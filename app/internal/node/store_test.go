package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := NewStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestStoreDuplicateAndNumberedConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for i, v := range []string{"one", "one", "two", "two"} {
		r, e := s.Receive(ctx, "hello.txt", strings.NewReader(v))
		if e != nil {
			t.Fatal(e)
		}
		if i == 1 || i == 3 {
			if r.Skipped != 1 {
				t.Fatalf("duplicate result: %+v", r)
			}
		}
	}
	files, e := s.List("")
	if e != nil || len(files) != 2 {
		t.Fatalf("files=%v err=%v", files, e)
	}
	b, _ := os.ReadFile(filepath.Join(s.Root, "hello.txt"))
	if string(b) != "one" {
		t.Fatal("original overwritten")
	}
}
func TestStoreSamePathAndDirectoryMerge(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	src := t.TempDir()
	os.Mkdir(filepath.Join(src, "docs"), 0700)
	os.WriteFile(filepath.Join(src, "docs", "中文.txt"), []byte("same"), 0600)
	for i := 0; i < 2; i++ {
		r, e := s.Import(ctx, "", []string{filepath.Join(src, "docs")})
		if e != nil {
			t.Fatal(e)
		}
		if i == 1 && r.Skipped != 1 {
			t.Fatalf("reimport %+v", r)
		}
	}
	r, e := s.Import(ctx, "", []string{filepath.Join(s.Root, "docs")})
	if e != nil || r.Skipped == 0 {
		t.Fatalf("same path %+v %v", r, e)
	}
	if _, e = s.Import(ctx, "", []string{s.Root}); e == nil {
		t.Fatal("accepted recursive root import")
	}
}
func TestStoreTraversalSymlinkAndConcurrentConflict(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	for _, p := range []string{"../outside", "/absolute", "a/../../b", "a\\b", "CON.txt"} {
		if _, e := s.Receive(ctx, p, strings.NewReader("x")); e == nil {
			t.Errorf("accepted unsafe path %q", p)
		}
	}
	out := t.TempDir()
	if e := os.Symlink(out, filepath.Join(s.Root, "link")); e == nil {
		if _, e = s.List("link"); e == nil {
			t.Fatal("followed symlink")
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Receive(ctx, "race.txt", strings.NewReader("same bytes")); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	fs, _ := s.List("")
	n := 0
	for _, f := range fs {
		if strings.HasPrefix(f.Name, "race") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("concurrent duplicate count %d", n)
	}
}
func TestStoreCaseCollisionAndEmptyFile(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Receive(ctx, "Hello.txt", strings.NewReader(""))
	r, e := s.Receive(ctx, "hello.txt", strings.NewReader(""))
	if e != nil || r.Skipped != 1 {
		t.Fatalf("casefold %+v %v", r, e)
	}
	r, e = s.Receive(ctx, "HELLO.txt", strings.NewReader("changed"))
	if e != nil || r.Renamed != 1 {
		t.Fatalf("case conflict %+v %v", r, e)
	}
}

func TestStoreRejectsSymlinkRoot(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(dir, "bucket")); e != nil {
		t.Skip(e)
	}
	if _, e := NewStore(dir); e == nil {
		t.Fatal("accepted symlink root")
	}
}
func TestStoreNumberedIncomingReimport(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Receive(ctx, "report (1).txt", strings.NewReader("A"))
	s.Receive(ctx, "report (1).txt", strings.NewReader("B"))
	r, e := s.Receive(ctx, "report (1).txt", strings.NewReader("B"))
	if e != nil || r.Skipped != 1 {
		t.Fatalf("numbered repeat %+v %v", r, e)
	}
}
