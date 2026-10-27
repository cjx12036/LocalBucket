package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"time"
)

type Job struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Status    string   `json:"status"`
	Message   string   `json:"message"`
	Error     string   `json:"error"`
	Completed int64    `json:"completed"`
	Total     int64    `json:"total"`
	Paths     []string `json:"paths"`
	Result    Result   `json:"result"`
	cancel    context.CancelFunc
	created   time.Time
}

func (s *Service) startJob(kind string, fn func(context.Context, string) (Result, []string, error)) string {
	ctx, cancel := context.WithCancel(s.ctx)
	id := randomID()
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		cancel()
		return ""
	}
	if len(s.jobs) > 80 {
		for k, j := range s.jobs {
			if j.Status != "running" {
				delete(s.jobs, k)
			}
		}
	}
	s.jobs[id] = &Job{ID: id, Kind: kind, Status: "running", Message: "正在准备…", cancel: cancel, created: time.Now()}
	s.jobWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.jobWG.Done()
		defer cancel()
		v, paths, e := fn(ctx, id)
		s.mu.Lock()
		defer s.mu.Unlock()
		j := s.jobs[id]
		if j == nil {
			return
		}
		j.Result = v
		j.Paths = paths
		if e != nil {
			j.Status = "error"
			j.Error = e.Error()
			j.Message = "操作失败：" + e.Error()
			if errors.Is(e, context.Canceled) {
				j.Status = "canceled"
				j.Message = "已取消"
			}
			return
		}
		j.Status = "done"
		j.Message = fmt.Sprintf("已添加 %d 个，跳过 %d 个重复文件，%d 个已保留为新名称", v.Added, v.Skipped, v.Renamed)
		if kind == "prepare" {
			j.Message = "文件已准备好，可以拖出复制"
		}
	}()
	return id
}
func (s *Service) getJob(id string) Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[id]; j != nil {
		return *j
	}
	return Job{}
}
func (s *Service) allJobs() []Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	js := make([]Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		v := *j
		v.Paths = append([]string(nil), j.Paths...)
		js = append(js, v)
	}
	sort.Slice(js, func(i, j int) bool { return js[i].created.Before(js[j].created) })
	return js
}
func (s *Service) progress(id, name string, n, total int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.jobs[id]; j != nil {
		j.Completed += n
		if total > 0 {
			j.Total += total
		}
		if name != "" {
			j.Message = name
		}
	}
}

type countReader struct {
	r      io.Reader
	onRead func(int)
}

func (r countReader) Read(b []byte) (int, error) {
	n, e := r.r.Read(b)
	if n > 0 {
		r.onRead(n)
	}
	return n, e
}
func (s *Service) importTo(ctx context.Context, jid string, p Peer, dest string, sources []string) (Result, error) {
	if _, e := cleanRel(dest); e != nil {
		return Result{}, e
	}
	if p.Local {
		s.progress(jid, "正在检查并添加文件…", 0, 0)
		return s.store.Import(ctx, dest, sources)
	}
	var total Result
	for _, src := range sources {
		v, e := s.uploadTree(ctx, jid, p, dest, src)
		total.add(v)
		if e != nil {
			return total, e
		}
	}
	return total, nil
}
func (s *Service) uploadTree(ctx context.Context, jid string, p Peer, dest, src string) (Result, error) {
	if e := ctx.Err(); e != nil {
		return Result{}, e
	}
	st, e := os.Lstat(src)
	if e != nil {
		return Result{}, e
	}
	if st.Mode()&os.ModeSymlink != 0 || isReparse(src, st) {
		return Result{}, errors.New("不支持导入链接")
	}
	name := filepath.Base(src)
	if !validName(name) {
		return Result{}, errors.New("文件名不受支持：" + name)
	}
	rel := path.Join(dest, name)
	if st.IsDir() {
		var v struct {
			Path string `json:"path"`
		}
		if e = s.remoteJSON(ctx, p, "POST", "/v1/mkdir?path="+query(rel), nil, &v); e != nil {
			return Result{}, e
		}
		var r Result
		cs, e := os.ReadDir(src)
		if e != nil {
			return r, e
		}
		for _, c := range cs {
			n, e := s.uploadTree(ctx, jid, p, v.Path, filepath.Join(src, c.Name()))
			r.add(n)
			if e != nil {
				return r, e
			}
		}
		return r, nil
	}
	if !st.Mode().IsRegular() {
		return Result{}, errors.New("不支持这个文件类型")
	}
	f, e := os.Open(src)
	if e != nil {
		return Result{}, e
	}
	defer f.Close()
	s.progress(jid, "正在添加 "+name, 0, st.Size())
	reader := countReader{f, func(n int) { s.progress(jid, "", int64(n), 0) }}
	req, e := http.NewRequestWithContext(ctx, "POST", p.Address+"/v1/upload?path="+query(rel), reader)
	if e != nil {
		return Result{}, e
	}
	req.ContentLength = st.Size()
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		return Result{}, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var v map[string]string
		json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&v)
		return Result{}, fmt.Errorf("添加失败：%s", v["error"])
	}
	var result Result
	e = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&result)
	return result, e
}
func (s *Service) prepare(ctx context.Context, jid string, p Peer, paths []string) ([]string, error) {
	out := make([]string, 0, len(paths))
	if p.Local {
		for _, rel := range paths {
			if rel == "" {
				return nil, errors.New("请先选择文件")
			}
			abs, e := s.store.resolve(rel)
			if e != nil {
				return nil, e
			}
			if _, e = os.Stat(abs); e != nil {
				return nil, e
			}
			out = append(out, abs)
		}
		return out, nil
	}
	cache := filepath.Join(s.DataDir, "cache")
	if e := os.MkdirAll(cache, 0700); e != nil {
		return nil, e
	}
	dir, e := os.MkdirTemp(cache, "export-")
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	for _, rel := range paths {
		if rel == "" {
			return nil, errors.New("请先选择文件")
		}
		if _, e = cleanRel(rel); e != nil {
			return nil, e
		}
		dest := filepath.Join(dir, path.Base(rel))
		if e = s.downloadTree(ctx, jid, p, rel, dest); e != nil {
			return nil, e
		}
		out = append(out, dest)
	}
	ok = true
	return out, nil
}
func (s *Service) downloadTree(ctx context.Context, jid string, p Peer, rel, dest string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	files, e := s.files(ctx, p, path.Dir(rel))
	if e != nil {
		return e
	}
	var selected *File
	for i := range files {
		if files[i].Path == rel {
			selected = &files[i]
			break
		}
	}
	if selected == nil {
		return errors.New("对方文件已不存在，请刷新")
	}
	if selected.IsDir {
		if e = os.Mkdir(dest, 0700); e != nil {
			return e
		}
		children, e := s.files(ctx, p, rel)
		if e != nil {
			return e
		}
		for _, child := range children {
			if !validName(child.Name) || child.Path != path.Join(rel, child.Name) {
				return errors.New("对方返回了无效文件路径")
			}
			if e = s.downloadTree(ctx, jid, p, child.Path, filepath.Join(dest, child.Name)); e != nil {
				return e
			}
		}
		return nil
	}
	s.progress(jid, "正在下载 "+selected.Name, 0, selected.Size)
	req, e := http.NewRequestWithContext(ctx, "GET", p.Address+"/v1/file?path="+query(rel), nil)
	if e != nil {
		return e
	}
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("下载失败 (%d)", res.StatusCode)
	}
	f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(f, countReader{contextReader{ctx, res.Body}, func(n int) { s.progress(jid, "", int64(n), 0) }})
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func (s *Service) discoveryState() string {
	if s.discovery == nil {
		return "自动发现未启用"
	}
	return s.discovery.State()
}
