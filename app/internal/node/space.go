package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SpaceFile keeps physical ownership even when the native view has no peer layer.
type SpaceFile struct {
	File
	Peer  string `json:"peer"`
	Owner string `json:"owner"`
	Local bool   `json:"local"`
}
type UnavailablePeer struct {
	Peer  string `json:"peer"`
	Owner string `json:"owner"`
	Error string `json:"error"`
}
type Space struct {
	Files       []SpaceFile       `json:"files"`
	Peers       []Peer            `json:"peers"`
	Unavailable []UnavailablePeer `json:"unavailable"`
}

func (s *Service) space(ctx context.Context) Space {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	peers := s.allPeers()
	out := Space{Files: []SpaceFile{}, Peers: append([]Peer(nil), peers...), Unavailable: []UnavailablePeer{}}
	sort.Slice(out.Peers, func(i, j int) bool {
		a, b := out.Peers[i], out.Peers[j]
		if a.Local != b.Local {
			return a.Local
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 8)
	peers = append([]Peer(nil), out.Peers...)
	for i, p := range peers {
		wg.Add(1)
		go func(i int, p Peer) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				mu.Lock()
				out.Peers[i].Online = false
				out.Unavailable = append(out.Unavailable, UnavailablePeer{p.ID, p.Name, "暂时无法连接"})
				mu.Unlock()
				return
			}
			fs, e := s.files(ctx, p, "")
			if e == nil {
				for _, f := range fs {
					if !validName(f.Name) || f.Path != f.Name {
						e = errors.New("文件列表包含无效路径")
						break
					}
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if e != nil {
				out.Peers[i].Online = false
				out.Unavailable = append(out.Unavailable, UnavailablePeer{p.ID, p.Name, "暂时无法读取文件"})
				return
			}
			for _, f := range fs {
				out.Files = append(out.Files, SpaceFile{f, p.ID, p.Name, p.Local})
			}
		}(i, p)
	}
	wg.Wait()
	sort.Slice(out.Files, func(i, j int) bool {
		a, b := out.Files[i], out.Files[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		if strings.ToLower(a.Name) != strings.ToLower(b.Name) {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		if a.Peer != b.Peer {
			return a.Peer < b.Peer
		}
		return a.Path < b.Path
	})
	sort.Slice(out.Unavailable, func(i, j int) bool { return out.Unavailable[i].Peer < out.Unavailable[j].Peer })
	return out
}

type Selection struct {
	Peer string `json:"peer"`
	Path string `json:"path"`
}
type ownedSelection struct {
	peer Peer
	rel  string
}

func (s *Service) resolveSelections(items []Selection) ([]ownedSelection, error) {
	if len(items) == 0 {
		return nil, errors.New("请选择文件")
	}
	out := make([]ownedSelection, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		rel, e := cleanRel(item.Path)
		if e != nil || rel == "" {
			return nil, errors.New("无效的文件选择")
		}
		p, e := s.peer(item.Peer)
		if e != nil {
			return nil, e
		}
		key := p.ID + "/" + rel
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ownedSelection{p, rel})
	}
	return out, nil
}

// Batch preparation uses one cache namespace: distinct same-name folders must
// remain separate, unlike normal folder imports which intentionally merge.
func (s *Service) prepareSelections(ctx context.Context, jid string, items []ownedSelection) (paths []string, err error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	cache := filepath.Join(s.DataDir, "cache")
	if e := os.MkdirAll(cache, 0700); e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp(cache, "selection-")
	if e != nil {
		return nil, e
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	names := map[string]bool{}
	for _, item := range items {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		base := path.Base(item.rel)
		name := base
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		for n := 1; names[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		names[strings.ToLower(name)] = true
		dest := filepath.Join(dir, name)
		if e = s.downloadTree(ctx, jid, item.peer, item.rel, dest); e != nil {
			return nil, e
		}
		paths = append(paths, dest)
	}
	return paths, nil
}
