package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

const Version = "0.2.0"

type Options struct {
	DataDir, DufsPath, Name string
	Port                    int
	NoDiscovery             bool
}
type Peer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Local   bool   `json:"local"`
	Online  bool   `json:"online"`
	Version string `json:"version"`
	seen    time.Time
}
type Service struct {
	AdminURL, Token, BucketDir, DataDir string
	Self                                Peer
	store                               *Store
	ctx                                 context.Context
	cancel                              context.CancelFunc
	once                                sync.Once
	admin, lan                          *http.Server
	adminLn, lanLn                      net.Listener
	lanURL, dufsURL                     string
	dufs                                *exec.Cmd
	dufsDone                            chan error
	log                                 *os.File
	mu                                  sync.Mutex
	peers                               map[string]Peer
	jobs                                map[string]*Job
	guideSeen                           bool
	discovery                           *Discovery
	jobWG                               sync.WaitGroup
	closing                             bool
}

func randomID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func Start(o Options) (s *Service, err error) {
	if o.DataDir == "" {
		p, e := os.UserConfigDir()
		if e != nil {
			return nil, e
		}
		o.DataDir = filepath.Join(p, "LocalBucket")
	}
	if e := os.MkdirAll(o.DataDir, 0700); e != nil {
		return nil, e
	}
	st, e := NewStore(o.DataDir)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	s = &Service{DataDir: o.DataDir, BucketDir: st.Root, Token: randomID() + randomID(), store: st, ctx: ctx, cancel: cancel, peers: map[string]Peer{}, jobs: map[string]*Job{}}
	owned := s
	defer func() {
		if err != nil {
			owned.Close()
		}
	}()
	s.loadConfig()
	if o.Name == "" {
		o.Name, _ = os.Hostname()
	}
	s.Self.Name = o.Name
	s.Self.Local = true
	s.Self.Online = true
	s.Self.Version = Version
	if e = s.saveConfig(); e != nil {
		return nil, e
	}
	port := o.Port
	if port == 0 {
		port = 47931
	} else if port < 0 {
		port = 0
	}
	s.lanLn, e = net.Listen("tcp4", fmt.Sprintf("0.0.0.0:%d", port))
	if e != nil {
		return nil, fmt.Errorf("共享端口 %d 已被占用，请先关闭旧版 LocalBucket：%w", port, e)
	}
	actual := s.lanLn.Addr().(*net.TCPAddr).Port
	s.lanURL = fmt.Sprintf("http://127.0.0.1:%d", actual)
	s.Self.Address = s.lanURL
	s.adminLn, e = net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	s.AdminURL = "http://" + s.adminLn.Addr().String()
	if o.DufsPath != "" {
		if e = s.startDufs(o.DufsPath); e != nil {
			return nil, e
		}
	}
	s.lan = &http.Server{Handler: s.lanHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	s.admin = &http.Server{Handler: s.adminHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go s.lan.Serve(s.lanLn)
	go s.admin.Serve(s.adminLn)
	if !o.NoDiscovery {
		s.discovery = startDiscovery(s, actual)
	}
	return s, nil
}
func (s *Service) loadConfig() {
	var c struct {
		ID    string `json:"id"`
		Guide bool   `json:"guide_seen"`
	}
	b, _ := os.ReadFile(filepath.Join(s.DataDir, "config.json"))
	json.Unmarshal(b, &c)
	if len(c.ID) != 32 {
		c.ID = randomID()
	}
	s.Self.ID = c.ID
	s.guideSeen = c.Guide
}
func (s *Service) saveConfig() error {
	b, e := json.Marshal(map[string]any{"id": s.Self.ID, "guide_seen": s.guideSeen})
	if e != nil {
		return e
	}
	p := filepath.Join(s.DataDir, "config.json")
	tmp := p + ".tmp"
	if e = os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	return os.Rename(tmp, p)
}
func (s *Service) startDufs(binary string) error {
	ln, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	s.dufsURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	s.log, _ = os.OpenFile(filepath.Join(s.DataDir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	s.dufs = exec.CommandContext(s.ctx, binary, s.BucketDir, "-b", "127.0.0.1", "-p", fmt.Sprint(port), "--allow-archive")
	hideProcess(s.dufs)
	if s.log != nil {
		s.dufs.Stdout = s.log
		s.dufs.Stderr = s.log
	}
	if e = s.dufs.Start(); e != nil {
		return fmt.Errorf("文件服务无法启动：%w", e)
	}
	s.dufsDone = make(chan error, 1)
	go func() { s.dufsDone <- s.dufs.Wait() }()
	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case e := <-s.dufsDone:
			return fmt.Errorf("文件服务提前退出：%v", e)
		default:
		}
		r, e := client.Get(s.dufsURL + "/__dufs__/health")
		if e == nil {
			r.Body.Close()
			if r.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	return errors.New("文件服务启动超时")
}
func (s *Service) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.cancel()
		if s.discovery != nil {
			s.discovery.Close()
		}
		if s.admin != nil {
			s.admin.Close()
		} else if s.adminLn != nil {
			s.adminLn.Close()
		}
		if s.lan != nil {
			s.lan.Close()
		} else if s.lanLn != nil {
			s.lanLn.Close()
		}
		if s.dufs != nil && s.dufs.Process != nil {
			s.dufs.Process.Kill()
			if s.dufsDone != nil {
				select {
				case <-s.dufsDone:
				case <-time.After(2 * time.Second):
				}
			}
		}
		if s.log != nil {
			s.log.Close()
		}
		s.jobWG.Wait()
	})
	return nil
}
func (s *Service) peer(id string) (Peer, error) {
	if id == s.Self.ID || id == "" {
		return s.Self, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.peers[id]
	if !ok || time.Since(p.seen) > 12*time.Second {
		return Peer{}, errors.New("文件所在电脑已离线，请返回共享空间刷新")
	}
	return p, nil
}
func (s *Service) allPeers() []Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := []Peer{s.Self}
	for id, p := range s.peers {
		if time.Since(p.seen) > 12*time.Second {
			delete(s.peers, id)
			continue
		}
		ps = append(ps, p)
	}
	return ps
}
func legacyDir() string {
	base, _ := os.UserConfigDir()
	if runtime.GOOS == "windows" {
		base = os.Getenv("LOCALAPPDATA")
	}
	return filepath.Join(base, "LocalBucketDragSpike", "bucket")
}
func (s *Service) files(ctx context.Context, p Peer, rel string) ([]File, error) {
	if p.Local {
		return s.store.List(rel)
	}
	var v struct {
		Files []File `json:"files"`
	}
	e := s.remoteJSON(ctx, p, "GET", "/v1/list?path="+query(rel), nil, &v)
	return v.Files, e
}
func (s *Service) remoteJSON(ctx context.Context, p Peer, method, endpoint string, body io.Reader, out any) error {
	req, e := http.NewRequestWithContext(ctx, method, p.Address+endpoint, body)
	if e != nil {
		return e
	}
	res, e := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var v map[string]string
		json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&v)
		return fmt.Errorf("对方电脑：%s (%d)", v["error"], res.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
	}
	return nil
}
