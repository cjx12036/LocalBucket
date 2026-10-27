package node

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func spaceRequest(t *testing.T, s *Service, method, route, body string) (int, []byte) {
	t.Helper()
	r, _ := http.NewRequest(method, s.AdminURL+route, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+s.Token)
	res, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	b, e := io.ReadAll(res.Body)
	if e != nil {
		t.Fatal(e)
	}
	return res.StatusCode, b
}
func addSpacePeer(s, other *Service) {
	p := other.Self
	p.Local = false
	p.seen = time.Now()
	s.mu.Lock()
	s.peers[p.ID] = p
	s.mu.Unlock()
}
func waitSpaceJob(t *testing.T, s *Service, b []byte) Job {
	t.Helper()
	var v map[string]string
	if e := json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	if v["job"] == "" {
		t.Fatalf("missing job: %s", b)
	}
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		j := s.getJob(v["job"])
		if j.Status != "running" {
			if j.Status != "done" {
				t.Fatalf("job: %+v", j)
			}
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job timeout")
	return Job{}
}
func TestSpaceKeepsOwnerCollisionsAndHealthyPeers(t *testing.T) {
	s, other := startTest(t), startTest(t)
	s.Self.Name = "Mac"
	other.Self.Name = "Windows"
	addSpacePeer(s, other)
	for _, n := range []*Service{s, other} {
		if e := os.WriteFile(filepath.Join(n.BucketDir, "same.txt"), []byte(n.Self.Name), 0600); e != nil {
			t.Fatal(e)
		}
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	s.mu.Lock()
	s.peers["slow"] = Peer{ID: "slow", Name: "Offline", Address: slow.URL, Online: true, seen: time.Now()}
	s.mu.Unlock()
	started := time.Now()
	code, b := spaceRequest(t, s, "GET", "/app/space", "")
	if code != 200 {
		t.Fatalf("space status %d: %s", code, b)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("slow peer blocked shared view")
	}
	var v struct {
		Files []struct {
			Name, Peer, Owner string
			Local             bool
		}
		Unavailable []struct{ Peer string }
	}
	if e := json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	if len(v.Files) != 2 || len(v.Unavailable) != 1 {
		t.Fatalf("partial list: %s", b)
	}
	got := map[string]string{}
	for _, f := range v.Files {
		got[f.Peer] = f.Owner
		if f.Name != "same.txt" {
			t.Fatal(f)
		}
		if f.Local != (f.Peer == s.Self.ID) {
			t.Fatal("wrong local identity")
		}
	}
	if got[s.Self.ID] != "Mac" || got[other.Self.ID] != "Windows" || v.Unavailable[0].Peer != "slow" {
		t.Fatalf("lost ownership: %s", b)
	}
}
func TestMixedOwnerPrepareAndSavePreserveDistinctFolders(t *testing.T) {
	s, other := startTest(t), startTest(t)
	addSpacePeer(s, other)
	for _, n := range []*Service{s, other} {
		os.Mkdir(filepath.Join(n.BucketDir, "Folder"), 0700)
		os.WriteFile(filepath.Join(n.BucketDir, "Folder", "same.txt"), []byte(n.Self.ID), 0600)
	}
	items := []map[string]string{{"peer": s.Self.ID, "path": "Folder"}, {"peer": other.Self.ID, "path": "Folder"}}
	body, _ := json.Marshal(map[string]any{"items": items})
	code, b := spaceRequest(t, s, "POST", "/app/prepare", string(body))
	if code != 200 {
		t.Fatalf("prepare %d: %s", code, b)
	}
	j := waitSpaceJob(t, s, b)
	if len(j.Paths) != 2 || j.Paths[0] == j.Paths[1] || filepath.Base(j.Paths[0]) == filepath.Base(j.Paths[1]) {
		t.Fatalf("collapsed names: %+v", j.Paths)
	}
	for i, p := range j.Paths {
		contents, e := os.ReadFile(filepath.Join(p, "same.txt"))
		if e != nil || string(contents) != items[i]["peer"] {
			t.Fatalf("wrong contents: %s %v", contents, e)
		}
	}
	dest := t.TempDir()
	body, _ = json.Marshal(map[string]any{"items": items, "destination": dest})
	code, b = spaceRequest(t, s, "POST", "/app/save", string(body))
	if code != 200 {
		t.Fatal(string(b))
	}
	waitSpaceJob(t, s, b)
	fs, e := os.ReadDir(dest)
	if e != nil || len(fs) != 2 {
		t.Fatalf("save merged same-name folders: %v %v", fs, e)
	}
	for _, n := range []*Service{s, other} {
		if _, e := os.Stat(filepath.Join(n.BucketDir, "Folder", "same.txt")); e != nil {
			t.Fatal("source removed", e)
		}
	}
}
func TestMixedOwnerInvalidSelectionRejected(t *testing.T) {
	s := startTest(t)
	for _, body := range []string{`{"items":[]}`, `{"items":[{"peer":"","path":"../secret"}]}`, `{"items":[{"peer":"","path":"."}]}`, `{"items":[{"peer":"missing","path":"x"}]}`} {
		code, b := spaceRequest(t, s, "POST", "/app/prepare", body)
		if code < 400 {
			t.Fatalf("invalid selection accepted %s: %s", body, b)
		}
	}
}

func TestMixedOwnerCancellationRemovesPartialCache(t *testing.T) {
	s := startTest(t)
	began := make(chan struct{}, 1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/list" {
			writeJSON(w, map[string]any{"files": []File{{Name: "large.bin", Path: "large.bin", Size: 1024}}})
			return
		}
		began <- struct{}{}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer remote.Close()
	s.mu.Lock()
	s.peers["remote"] = Peer{ID: "remote", Name: "Windows", Address: remote.URL, Online: true, seen: time.Now()}
	s.mu.Unlock()
	code, b := spaceRequest(t, s, "POST", "/app/prepare", `{"items":[{"peer":"remote","path":"large.bin"}]}`)
	if code != 200 {
		t.Fatal(string(b))
	}
	var v map[string]string
	json.Unmarshal(b, &v)
	select {
	case <-began:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not begin")
	}
	spaceRequest(t, s, "POST", "/app/cancel", `{"job":"`+v["job"]+`"}`)
	deadline := time.Now().Add(3 * time.Second)
	for s.getJob(v["job"]).Status == "running" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if j := s.getJob(v["job"]); j.Status != "canceled" {
		t.Fatalf("not canceled: %+v", j)
	}
	entries, e := os.ReadDir(filepath.Join(s.DataDir, "cache"))
	if e != nil || len(entries) != 0 {
		t.Fatalf("partial cache remains: %v %v", entries, e)
	}
}

func TestMixedOwnerSaveRemovesTemporaryCopies(t *testing.T) {
	s := startTest(t)
	os.WriteFile(filepath.Join(s.BucketDir, "a.txt"), []byte("original"), 0600)
	for _, dest := range []string{t.TempDir(), "relative-invalid"} {
		body, _ := json.Marshal(map[string]any{"items": []Selection{{Peer: s.Self.ID, Path: "a.txt"}}, "destination": dest})
		code, b := spaceRequest(t, s, "POST", "/app/save", string(body))
		if code != 200 {
			t.Fatal(string(b))
		}
		var v map[string]string
		json.Unmarshal(b, &v)
		deadline := time.Now().Add(3 * time.Second)
		for s.getJob(v["job"]).Status == "running" && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		j := s.getJob(v["job"])
		want := "done"
		if dest == "relative-invalid" {
			want = "error"
		}
		if j.Status != want {
			t.Fatalf("status %+v", j)
		}
		entries, e := os.ReadDir(filepath.Join(s.DataDir, "cache"))
		if e != nil && !os.IsNotExist(e) {
			t.Fatal(e)
		}
		if len(entries) != 0 {
			t.Fatalf("save leaked copies: %v", entries)
		}
	}
}
