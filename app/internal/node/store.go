package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type File struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	IsDir    bool   `json:"is_dir"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}
type Result struct {
	Added   int `json:"added"`
	Skipped int `json:"skipped"`
	Renamed int `json:"renamed"`
}

func (r *Result) add(v Result) { r.Added += v.Added; r.Skipped += v.Skipped; r.Renamed += v.Renamed }

type Store struct {
	Root    string
	staging string
	mu      sync.Mutex
}

func NewStore(data string) (*Store, error) {
	root := filepath.Join(data, "bucket")
	staging := filepath.Join(data, "staging")
	for _, p := range []string{root, staging} {
		if st, e := os.Lstat(p); e == nil {
			if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || isReparse(p, st) {
				return nil, errors.New("bucket 或临时目录不能是链接")
			}
		} else if !os.IsNotExist(e) {
			return nil, e
		}
		if e := os.MkdirAll(p, 0700); e != nil {
			return nil, e
		}
	}
	abs, e := filepath.EvalSymlinks(root)
	if e != nil {
		return nil, e
	}
	return &Store{Root: abs, staging: staging}, nil
}

func validName(n string) bool {
	if n == "" || n == "." || n == ".." || strings.TrimRight(n, " .") != n || strings.ContainsAny(n, "<>:\"\\|?*\x00/") {
		return false
	}
	for _, c := range n {
		if c < 32 {
			return false
		}
	}
	base := strings.ToUpper(strings.Split(n, ".")[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}
func cleanRel(rel string) (string, error) {
	if rel == "" || rel == "." {
		return "", nil
	}
	if strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return "", errors.New("文件路径无效")
	}
	for _, part := range strings.Split(rel, "/") {
		if !validName(part) {
			return "", errors.New("文件名或路径不受支持")
		}
	}
	return rel, nil
}
func (s *Store) resolve(rel string) (string, error) {
	rootInfo, rootErr := os.Lstat(s.Root)
	if rootErr != nil {
		return "", rootErr
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 || isReparse(s.Root, rootInfo) {
		return "", errors.New("本机 bucket 已被替换为链接，请恢复存储目录")
	}
	rel, e := cleanRel(rel)
	if e != nil {
		return "", e
	}
	cur := s.Root
	if rel == "" {
		return cur, nil
	}
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		st, e := os.Lstat(cur)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return "", e
		}
		if st.Mode()&os.ModeSymlink != 0 || isReparse(cur, st) {
			return "", errors.New("不支持符号链接或重解析目录")
		}
	}
	return cur, nil
}
func (s *Store) List(rel string) ([]File, error) {
	p, e := s.resolve(rel)
	if e != nil {
		return nil, e
	}
	items, e := os.ReadDir(p)
	if e != nil {
		return nil, e
	}
	fs := make([]File, 0, len(items))
	for _, it := range items {
		st, e := it.Info()
		if e != nil || st.Mode()&os.ModeSymlink != 0 || isReparse(filepath.Join(p, it.Name()), st) || (!st.Mode().IsRegular() && !st.IsDir()) {
			continue
		}
		fs = append(fs, File{Name: it.Name(), Path: path.Join(rel, it.Name()), IsDir: it.IsDir(), Size: st.Size(), Modified: st.ModTime().Unix()})
	}
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].IsDir != fs[j].IsDir {
			return fs[i].IsDir
		}
		return strings.ToLower(fs[i].Name) < strings.ToLower(fs[j].Name)
	})
	return fs, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}
func digest(ctx context.Context, p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	_, e = io.Copy(h, contextReader{ctx, f})
	return hex.EncodeToString(h.Sum(nil)), e
}

var numbered = regexp.MustCompile(` \([1-9][0-9]*\)$`)

func group(n string) string {
	ext := filepath.Ext(n)
	stem := strings.TrimSuffix(n, ext)
	for {
		next := numbered.ReplaceAllString(stem, "")
		if next == stem {
			break
		}
		stem = next
	}
	return strings.ToLower(stem + ext)
}
func matchingName(dir, n string) (string, error) {
	items, e := os.ReadDir(dir)
	if e != nil {
		return "", e
	}
	for _, it := range items {
		if strings.EqualFold(it.Name(), n) {
			return it.Name(), nil
		}
	}
	return "", nil
}
func unused(dir, name string) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 0; i < 100000; i++ {
		n := name
		if i > 0 {
			n = base + " (" + strconv.Itoa(i) + ")" + ext
		}
		exists, e := matchingName(dir, n)
		if e != nil {
			return "", e
		}
		if exists == "" {
			return n, nil
		}
	}
	return "", errors.New("同名文件过多")
}

func (s *Store) Receive(ctx context.Context, rel string, reader io.Reader) (Result, error) {
	rel, e := cleanRel(rel)
	if e != nil || rel == "" {
		return Result{}, errors.New("文件路径无效")
	}
	f, e := os.CreateTemp(s.staging, "upload-")
	if e != nil {
		return Result{}, e
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	h := sha256.New()
	size, e := io.Copy(io.MultiWriter(f, h), contextReader{ctx, reader})
	ce := f.Close()
	if e != nil {
		return Result{}, e
	}
	if ce != nil {
		return Result{}, ce
	}
	if e = ctx.Err(); e != nil {
		return Result{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dest, e := s.resolve(path.Dir(rel))
	if e != nil {
		return Result{}, e
	}
	if e = os.MkdirAll(dest, 0700); e != nil {
		return Result{}, e
	}
	name := path.Base(rel)
	items, e := os.ReadDir(dest)
	if e != nil {
		return Result{}, e
	}
	hash := hex.EncodeToString(h.Sum(nil))
	for _, it := range items {
		if group(it.Name()) != group(name) || it.IsDir() {
			continue
		}
		p := filepath.Join(dest, it.Name())
		st, e := os.Lstat(p)
		if e != nil || !st.Mode().IsRegular() || isReparse(p, st) || st.Size() != size {
			continue
		}
		old, e := digest(ctx, p)
		if e != nil {
			return Result{}, e
		}
		if old == hash {
			return Result{Skipped: 1}, nil
		}
	}
	n, e := unused(dest, name)
	if e != nil {
		return Result{}, e
	}
	if e = ctx.Err(); e != nil {
		return Result{}, e
	}
	target := filepath.Join(dest, n)
	// Hard-link commit is exclusive and atomic: an external writer cannot be overwritten.
	for i := 0; i < 100; i++ {
		e = os.Link(tmp, target)
		if e == nil {
			r := Result{Added: 1}
			if n != name {
				r.Renamed = 1
			}
			return r, nil
		}
		if !os.IsExist(e) {
			return Result{}, e
		}
		n, e = unused(dest, name)
		if e != nil {
			return Result{}, e
		}
		target = filepath.Join(dest, n)
	}
	return Result{}, errors.New("文件冲突，请重试")
}
func (s *Store) Mkdir(rel string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rel, e := cleanRel(rel)
	if e != nil {
		return "", e
	}
	if rel == "" {
		return "", nil
	}
	parent, e := s.resolve(path.Dir(rel))
	if e != nil {
		return "", e
	}
	if e = os.MkdirAll(parent, 0700); e != nil {
		return "", e
	}
	name := path.Base(rel)
	match, e := matchingName(parent, name)
	if e != nil {
		return "", e
	}
	if match != "" {
		st, e := os.Lstat(filepath.Join(parent, match))
		if e != nil {
			return "", e
		}
		if st.IsDir() && st.Mode()&os.ModeSymlink == 0 && !isReparse(filepath.Join(parent, match), st) {
			return path.Join(path.Dir(rel), match), nil
		}
	}
	n, e := unused(parent, name)
	if e != nil {
		return "", e
	}
	if e = os.Mkdir(filepath.Join(parent, n), 0700); e != nil {
		return "", e
	}
	return path.Join(path.Dir(rel), n), nil
}
func within(parent, p string) bool {
	r, e := filepath.Rel(parent, p)
	return e == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)))
}
func (s *Store) Import(ctx context.Context, dest string, sources []string) (Result, error) {
	var result Result
	destPath, e := s.resolve(dest)
	if e != nil {
		return result, e
	}
	for _, src := range sources {
		abs, e := filepath.Abs(src)
		if e != nil {
			return result, e
		}
		st, e := os.Lstat(abs)
		if e != nil {
			return result, e
		}
		if st.Mode()&os.ModeSymlink != 0 || isReparse(abs, st) {
			return result, errors.New("不支持导入符号链接")
		}
		canonical, e := filepath.EvalSymlinks(abs)
		if e != nil {
			return result, e
		}
		if st.IsDir() && within(canonical, destPath) {
			return result, errors.New("不能把目录导入自身或其子目录")
		}
		target := filepath.Join(destPath, filepath.Base(canonical))
		if same, e := os.Stat(target); e == nil && os.SameFile(st, same) {
			result.Skipped++
			continue
		}
		r, e := s.importOne(ctx, dest, canonical)
		result.add(r)
		if e != nil {
			return result, e
		}
	}
	return result, nil
}
func (s *Store) importOne(ctx context.Context, dest, src string) (Result, error) {
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
		return Result{}, fmt.Errorf("不支持的文件名：%s", name)
	}
	if st.IsDir() {
		rel, e := s.Mkdir(path.Join(dest, name))
		if e != nil {
			return Result{}, e
		}
		var r Result
		cs, e := os.ReadDir(src)
		if e != nil {
			return r, e
		}
		for _, c := range cs {
			v, e := s.importOne(ctx, rel, filepath.Join(src, c.Name()))
			r.add(v)
			if e != nil {
				return r, e
			}
		}
		return r, nil
	}
	if !st.Mode().IsRegular() {
		return Result{}, errors.New("只支持普通文件与文件夹")
	}
	f, e := os.Open(src)
	if e != nil {
		return Result{}, e
	}
	defer f.Close()
	return s.Receive(ctx, path.Join(dest, name), f)
}
