package node

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func query(s string) string { return url.QueryEscape(s) }
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, e error, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": e.Error()})
}
func method(w http.ResponseWriter, r *http.Request, want string) bool {
	if r.Method != want {
		fail(w, errors.New("不支持这个操作"), 405)
		return false
	}
	return true
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if !method(w, r, "POST") {
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, errors.New("请求格式无效"), 400)
		return false
	}
	return true
}
func (s *Service) lanHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/info", func(w http.ResponseWriter, r *http.Request) {
		if method(w, r, "GET") {
			p := s.Self
			p.Local = false
			writeJSON(w, p)
		}
	})
	mux.HandleFunc("/v1/list", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, "GET") {
			return
		}
		fs, e := s.store.List(r.URL.Query().Get("path"))
		if e != nil {
			fail(w, e, 400)
			return
		}
		writeJSON(w, map[string]any{"files": fs})
	})
	mux.HandleFunc("/v1/file", func(w http.ResponseWriter, r *http.Request) {
		if method(w, r, "GET") {
			s.serveLocalFile(w, r, r.URL.Query().Get("path"))
		}
	})
	mux.HandleFunc("/v1/upload", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, "POST") {
			return
		}
		rel := r.URL.Query().Get("path")
		if _, e := cleanRel(rel); e != nil {
			fail(w, e, 400)
			return
		}
		result, e := s.store.Receive(r.Context(), rel, r.Body)
		if e != nil {
			fail(w, e, 400)
			return
		}
		writeJSON(w, result)
	})
	mux.HandleFunc("/v1/mkdir", func(w http.ResponseWriter, r *http.Request) {
		if !method(w, r, "POST") {
			return
		}
		rel, e := s.store.Mkdir(r.URL.Query().Get("path"))
		if e != nil {
			fail(w, e, 400)
			return
		}
		writeJSON(w, map[string]string{"path": rel})
	})
	return mux
}
func (s *Service) adminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() || r.Header.Get("Authorization") != "Bearer "+s.Token || r.Header.Get("Origin") != "" {
			fail(w, errors.New("仅限本机 App 管理"), 401)
			return
		}
		if r.Method == "GET" {
			s.adminGet(w, r)
		} else if r.Method == "POST" {
			s.adminPost(w, r)
		} else {
			fail(w, errors.New("不支持这个操作"), 405)
		}
	})
}
func (s *Service) adminGet(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/app/peers":
		writeJSON(w, map[string]any{"peers": s.allPeers()})
	case "/app/list":
		p, e := s.peer(r.URL.Query().Get("peer"))
		if e != nil {
			fail(w, e, 404)
			return
		}
		fs, e := s.files(r.Context(), p, r.URL.Query().Get("path"))
		if e != nil {
			fail(w, e, 400)
			return
		}
		writeJSON(w, map[string]any{"files": fs, "local": p.Local})
	case "/app/jobs":
		writeJSON(w, map[string]any{"jobs": s.allJobs()})
	case "/app/download":
		p, e := s.peer(r.URL.Query().Get("peer"))
		if e != nil {
			fail(w, e, 404)
			return
		}
		rel := r.URL.Query().Get("path")
		if p.Local {
			s.serveLocalFile(w, r, rel)
		} else {
			s.proxyRemoteFile(w, r, p, rel)
		}
	case "/app/config":
		_, e := os.Stat(legacyDir())
		s.mu.Lock()
		guide := s.guideSeen
		s.mu.Unlock()
		writeJSON(w, map[string]bool{"legacy_available": e == nil, "guide_seen": guide})
	case "/app/diagnostics":
		writeJSON(w, map[string]any{"message": "请确保两台电脑的 LocalBucket 都在运行，并允许系统的局域网连接。来宾 WiFi 的设备隔离会阻止互访。", "port": s.lanLn.Addr().(*net.TCPAddr).Port, "addresses": localAddresses(), "discovery": s.discoveryState()})
	default:
		fail(w, errors.New("接口不存在"), 404)
	}
}

type action struct {
	Peer        string   `json:"peer"`
	Path        string   `json:"path"`
	Paths       []string `json:"paths"`
	Sources     []string `json:"sources"`
	Destination string   `json:"destination"`
	Job         string   `json:"job"`
	GuideSeen   bool     `json:"guide_seen"`
}

func (s *Service) adminPost(w http.ResponseWriter, r *http.Request) {
	var a action
	if !decode(w, r, &a) {
		return
	}
	switch r.URL.Path {
	case "/app/config":
		s.mu.Lock()
		s.guideSeen = a.GuideSeen
		e := s.saveConfig()
		s.mu.Unlock()
		if e != nil {
			fail(w, e, 500)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "/app/cancel":
		s.mu.Lock()
		j := s.jobs[a.Job]
		if j != nil {
			j.cancel()
		}
		s.mu.Unlock()
		writeJSON(w, map[string]bool{"ok": true})
	case "/app/delete":
		if e := s.deleteLocal(a.Paths); e != nil {
			fail(w, e, 400)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	case "/app/import", "/app/prepare", "/app/save", "/app/migrate":
		if r.URL.Path == "/app/migrate" {
			a.Peer = s.Self.ID
			a.Sources = []string{legacyDir()}
		}
		p, e := s.peer(a.Peer)
		if e != nil {
			fail(w, e, 404)
			return
		}
		kind := "import"
		if strings.HasSuffix(r.URL.Path, "prepare") {
			kind = "prepare"
		}
		if strings.HasSuffix(r.URL.Path, "save") {
			kind = "save"
		}
		if kind == "import" && len(a.Sources) == 0 {
			fail(w, errors.New("请选择要添加的文件"), 400)
			return
		}
		if kind != "import" && len(a.Paths) == 0 {
			fail(w, errors.New("请选择文件"), 400)
			return
		}
		j := s.startJob(kind, func(ctx context.Context, jid string) (Result, []string, error) {
			if kind == "import" {
				if strings.HasSuffix(r.URL.Path, "migrate") {
					children, e := os.ReadDir(legacyDir())
					if e != nil {
						return Result{}, nil, e
					}
					a.Sources = nil
					for _, c := range children {
						a.Sources = append(a.Sources, filepath.Join(legacyDir(), c.Name()))
					}
				}
				v, e := s.importTo(ctx, jid, p, a.Path, a.Sources)
				return v, nil, e
			}
			ps, e := s.prepare(ctx, jid, p, a.Paths)
			if e != nil {
				return Result{}, nil, e
			}
			if kind == "save" {
				if !filepath.IsAbs(a.Destination) {
					return Result{}, nil, errors.New("请选择本机目标文件夹")
				}
				tempStore, e := NewExportStore(a.Destination, s.DataDir)
				if e != nil {
					return Result{}, nil, e
				}
				defer os.RemoveAll(tempStore.staging)
				v, e := tempStore.Import(ctx, "", ps)
				return v, nil, e
			}
			return Result{}, ps, nil
		})
		writeJSON(w, map[string]string{"job": j})
	default:
		fail(w, errors.New("接口不存在"), 404)
	}
}
func (s *Service) serveLocalFile(w http.ResponseWriter, r *http.Request, rel string) {
	p, e := s.store.resolve(rel)
	if e != nil {
		fail(w, e, 400)
		return
	}
	st, e := os.Stat(p)
	if e != nil {
		fail(w, e, 404)
		return
	}
	if !st.IsDir() && !st.Mode().IsRegular() {
		fail(w, errors.New("只支持普通文件与文件夹"), 400)
		return
	}
	if s.dufsURL != "" && !st.IsDir() && !strings.HasPrefix(strings.Split(rel, "/")[0], "__dufs") {
		u, _ := url.Parse(s.dufsURL)
		u.Path = "/" + rel
		if st.IsDir() {
			u.RawQuery = "zip"
		}
		req, e := http.NewRequestWithContext(r.Context(), "GET", u.String(), nil)
		if e != nil {
			fail(w, e, 400)
			return
		}
		if v := r.Header.Get("Range"); v != "" {
			req.Header.Set("Range", v)
		}
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			fail(w, e, 502)
			return
		}
		defer res.Body.Close()
		for _, k := range []string{"Content-Type", "Content-Length", "Content-Disposition", "Content-Range", "Accept-Ranges", "Last-Modified"} {
			if v := res.Header.Get(k); v != "" {
				w.Header().Set(k, v)
			}
		}
		w.WriteHeader(res.StatusCode)
		io.Copy(w, res.Body)
		return
	}
	if st.IsDir() {
		w.Header().Set("Content-Type", "application/zip")
		zw := zip.NewWriter(w)
		defer zw.Close()
		filepath.WalkDir(p, func(file string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			st, e := d.Info()
			if e != nil {
				return e
			}
			if st.Mode()&os.ModeSymlink != 0 || isReparse(file, st) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if file != p {
					name, _ := filepath.Rel(p, file)
					_, e := zw.Create(filepath.ToSlash(name) + "/")
					return e
				}
				return nil
			}
			name, _ := filepath.Rel(p, file)
			z, e := zw.Create(filepath.ToSlash(name))
			if e != nil {
				return e
			}
			f, e := os.Open(file)
			if e != nil {
				return e
			}
			defer f.Close()
			_, e = io.Copy(z, contextReader{r.Context(), f})
			return e
		})
		return
	}
	http.ServeFile(w, r, p)
}
func (s *Service) proxyRemoteFile(w http.ResponseWriter, r *http.Request, p Peer, rel string) {
	req, e := http.NewRequestWithContext(r.Context(), "GET", p.Address+"/v1/file?path="+query(rel), nil)
	if e != nil {
		fail(w, e, 400)
		return
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		fail(w, e, 502)
		return
	}
	defer res.Body.Close()
	for _, k := range []string{"Content-Length", "Content-Type", "Content-Disposition"} {
		if v := res.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
}
func NewExportStore(destination, data string) (*Store, error) {
	st, e := os.Stat(destination)
	if e != nil || !st.IsDir() {
		return nil, errors.New("目标文件夹不存在")
	}
	root, e := filepath.EvalSymlinks(destination)
	if e != nil {
		return nil, e
	}
	staging, e := os.MkdirTemp(destination, ".localbucket-export-")
	if e != nil {
		return nil, e
	}
	return &Store{Root: root, staging: staging}, nil
}
