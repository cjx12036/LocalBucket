package node

import (
	"errors"
	"os"
	"path/filepath"
)

func (s *Service) deleteLocal(paths []string) error {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	targets := make([]string, 0, len(paths))
	for _, rel := range paths {
		normalized, e := cleanRel(rel)
		if e != nil {
			return e
		}
		if normalized == "" {
			return errors.New("不能删除 bucket 根目录")
		}
		p, e := s.store.resolve(normalized)
		if e != nil {
			return e
		}
		if p == s.BucketDir || filepath.Dir(p) == p {
			return errors.New("不能删除根目录")
		}
		if _, e = os.Lstat(p); e != nil {
			return e
		}
		targets = append(targets, p)
	}
	for _, p := range targets {
		if e := trash(p); e != nil {
			return e
		}
	}
	return nil
}
