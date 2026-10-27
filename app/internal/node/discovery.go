package node

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/grandcat/zeroconf"
	"net"
	"strings"
	"sync"
	"time"
)

type Discovery struct {
	mu     sync.Mutex
	mdns   *zeroconf.Server
	udp    *net.UDPConn
	cancel context.CancelFunc
	state  string
}
type beacon struct {
	Magic string `json:"magic"`
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Port  int    `json:"port"`
}

func (d *Discovery) State() string { d.mu.Lock(); defer d.mu.Unlock(); return d.state }
func (d *Discovery) Close() {
	d.cancel()
	if d.udp != nil {
		d.udp.Close()
	}
	if d.mdns != nil {
		done := make(chan struct{})
		go func() { d.mdns.Shutdown(); close(done) }()
		select {
		case <-done:
		case <-time.After(400 * time.Millisecond):
		}
	}
}
func interfaces() []net.Interface {
	all, _ := net.Interfaces()
	out := []net.Interface{}
	for _, it := range all {
		n := strings.ToLower(it.Name)
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 || it.Flags&net.FlagPointToPoint != 0 || strings.Contains(n, "utun") || strings.Contains(n, "tailscale") || strings.Contains(n, "docker") {
			continue
		}
		out = append(out, it)
	}
	return out
}
func localAddresses() []string {
	out := []string{}
	for _, it := range interfaces() {
		as, _ := it.Addrs()
		for _, a := range as {
			ip, _, e := net.ParseCIDR(a.String())
			if e == nil && ip.To4() != nil {
				out = append(out, ip.String())
			}
		}
	}
	return out
}
func broadcasts() []net.IP {
	out := []net.IP{net.IPv4bcast}
	for _, it := range interfaces() {
		as, _ := it.Addrs()
		for _, a := range as {
			ip, n, e := net.ParseCIDR(a.String())
			if e != nil || ip.To4() == nil || len(n.Mask) != 4 {
				continue
			}
			ip = ip.To4()
			b := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				b[i] = ip[i] | ^n.Mask[i]
			}
			out = append(out, b)
		}
	}
	return out
}
func startDiscovery(s *Service, port int) *Discovery {
	ctx, cancel := context.WithCancel(s.ctx)
	d := &Discovery{cancel: cancel, state: "正在自动寻找附近电脑"}
	ifaces := interfaces()
	m, e := zeroconf.Register("LocalBucket-"+s.Self.ID, "_localbucket._tcp", "local.", port, []string{"id=" + s.Self.ID, "version=" + Version}, ifaces)
	if e == nil {
		d.mdns = m
	}
	udp, e := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if e == nil {
		d.udp = udp
	}
	if d.mdns == nil && d.udp == nil {
		d.state = "自动发现无法启动，请检查系统网络授权"
	}
	slots := make(chan struct{}, 8)
	seen := map[string]time.Time{}
	targets := map[string]struct {
		ip   net.IP
		port int
	}{}
	var mu sync.Mutex
	verify := func(ip net.IP, p int) {
		if ip == nil || ip.To4() == nil || p < 1 || p > 65535 || (!ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLoopback()) {
			return
		}
		addr := fmt.Sprintf("http://%s:%d", ip.String(), p)
		mu.Lock()
		if time.Since(seen[addr]) < time.Second {
			mu.Unlock()
			return
		}
		seen[addr] = time.Now()
		mu.Unlock()
		select {
		case slots <- struct{}{}:
		default:
			return
		}
		go func() {
			defer func() { <-slots }()
			var peer Peer
			if e := s.remoteJSON(ctx, Peer{Address: addr}, "GET", "/v1/info", nil, &peer); e != nil {
				return
			}
			if peer.ID == s.Self.ID || len(peer.ID) != 32 || peer.Version != Version || len(peer.Name) > 256 {
				return
			}
			peer.Address = addr
			peer.Local = false
			peer.Online = true
			peer.seen = time.Now()
			s.mu.Lock()
			s.peers[peer.ID] = peer
			s.mu.Unlock()
			mu.Lock()
			if len(targets) < 256 {
				targets[addr] = struct {
					ip   net.IP
					port int
				}{ip, p}
			}
			mu.Unlock()
		}()
	}
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				copyTargets := make([]struct {
					ip   net.IP
					port int
				}, 0, len(targets))
				for _, target := range targets {
					copyTargets = append(copyTargets, target)
				}
				mu.Unlock()
				for _, target := range copyTargets {
					verify(target.ip, target.port)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		if resolver, e := zeroconf.NewResolver(zeroconf.SelectIfaces(ifaces), zeroconf.SelectIPTraffic(zeroconf.IPv4)); e == nil {
			entries := make(chan *zeroconf.ServiceEntry, 16)
			if e = resolver.Browse(ctx, "_localbucket._tcp", "local.", entries); e == nil {
				go func() {
					for {
						select {
						case entry, ok := <-entries:
							if !ok {
								return
							}
							for _, ip := range entry.AddrIPv4 {
								verify(ip, entry.Port)
							}
						case <-ctx.Done():
							return
						}
					}
				}()
			}
		}
	}()
	if udp != nil {
		announce := func(kind string, target *net.UDPAddr) {
			data, _ := json.Marshal(beacon{Magic: "LocalBucket/1", Kind: kind, ID: s.Self.ID, Port: port})
			if target != nil {
				udp.WriteToUDP(data, target)
				return
			}
			for _, ip := range broadcasts() {
				udp.WriteToUDP(data, &net.UDPAddr{IP: ip, Port: port})
			}
		}
		go func() {
			buf := make([]byte, 2048)
			for {
				n, from, e := udp.ReadFromUDP(buf)
				if e != nil {
					return
				}
				var b beacon
				if json.Unmarshal(buf[:n], &b) != nil || b.Magic != "LocalBucket/1" || b.ID == s.Self.ID {
					continue
				}
				if b.Kind == "query" {
					announce("announce", from)
				}
				if b.Kind == "query" || b.Kind == "announce" {
					verify(from.IP, b.Port)
				}
			}
		}()
		go func() {
			announce("query", nil)
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					announce("query", nil)
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	return d
}
