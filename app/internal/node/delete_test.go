package node

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteRejectsNormalizedRootAndDoesNotPartiallyDelete(t *testing.T) {
	s := startTest(t)
	for _, p := range []string{"", ".", "..", "a/..", "/"} {
		if e := s.deleteLocal([]string{p}); e == nil {
			t.Fatalf("accepted root/unsafe deletion %q", p)
		}
	}
	file := filepath.Join(s.BucketDir, "keep.txt")
	os.WriteFile(file, []byte("keep"), 0600)
	if e := s.deleteLocal([]string{"keep.txt", "."}); e == nil {
		t.Fatal("accepted mixed root deletion")
	}
	if _, e := os.Stat(file); e != nil {
		t.Fatal("partial deletion before validation")
	}
}
