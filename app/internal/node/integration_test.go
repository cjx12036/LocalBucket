package node

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchiveFiltersLinksAndDufsReservedPath(t *testing.T) {
	binary := os.Getenv("LOCALBUCKET_TEST_DUFS")
	if binary == "" {
		t.Skip("real Dufs binary required")
	}
	s, e := Start(Options{DataDir: t.TempDir(), DufsPath: binary, Port: -1, NoDiscovery: true})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ctx := context.Background()
	s.store.Receive(ctx, "archive/normal.txt", strings.NewReader("normal"))
	s.store.Mkdir("archive/empty")
	if e = os.Symlink(filepath.Join(s.BucketDir, "archive/normal.txt"), filepath.Join(s.BucketDir, "archive/alias.txt")); e != nil {
		t.Skip(e)
	}
	r, e := http.Get(s.lanURL + "/v1/file?path=archive")
	if e != nil {
		t.Fatal(e)
	}
	data, _ := io.ReadAll(r.Body)
	r.Body.Close()
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		t.Fatal(e)
	}
	if len(z.File) != 2 || z.File[0].Name != "empty/" || z.File[1].Name != "normal.txt" {
		t.Fatalf("archive includes links: %+v", z.File)
	}
	s.store.Receive(ctx, "__dufs__/health", strings.NewReader("actual user bytes"))
	r, e = http.Get(s.lanURL + "/v1/file?path=__dufs__%2Fhealth")
	if e != nil {
		t.Fatal(e)
	}
	data, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if string(data) != "actual user bytes" {
		t.Fatalf("reserved path returned %q", data)
	}
}

func TestTwoNodesFolderUploadPrepareAndExport(t *testing.T) {
	a, b := startTest(t), startTest(t)
	peer := b.Self
	peer.Local = false
	peer.Address = b.lanURL
	peer.seen = time.Now()
	a.mu.Lock()
	a.peers[peer.ID] = peer
	a.mu.Unlock()
	src := t.TempDir()
	dir := filepath.Join(src, "资料")
	os.Mkdir(dir, 0700)
	os.Mkdir(filepath.Join(dir, "子目录"), 0700)
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("v1"), 0600)
	os.WriteFile(filepath.Join(dir, "子目录", "中文.txt"), []byte("nested"), 0600)
	for i := 0; i < 2; i++ {
		r, e := a.importTo(context.Background(), "", peer, "", []string{dir})
		if e != nil {
			t.Fatal(e)
		}
		if i == 1 && r.Skipped != 2 {
			t.Fatalf("remote dedup %+v", r)
		}
	}
	paths, e := a.prepare(context.Background(), "", peer, []string{"资料"})
	if e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(filepath.Join(paths[0], "子目录", "中文.txt"))
	if e != nil || string(got) != "nested" {
		t.Fatalf("prepared %q %v", got, e)
	}
	if _, e = os.Stat(filepath.Join(b.BucketDir, "资料", "hello.txt")); e != nil {
		t.Fatal("source removed")
	}
	export := t.TempDir()
	st, e := NewExportStore(export, a.DataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(st.staging)
	if _, e = st.Import(context.Background(), "", paths); e != nil {
		t.Fatal(e)
	}
}
func TestInterruptedReceiveDoesNotPublishPartial(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := s.Receive(ctx, "partial.txt", strings.NewReader("contents")); e == nil {
		t.Fatal("canceled upload accepted")
	}
	fs, _ := s.List("")
	if len(fs) != 0 {
		t.Fatal("published partial")
	}
	staged, _ := os.ReadDir(s.staging)
	if len(staged) != 0 {
		t.Fatal("leaked staged file")
	}
}
func TestAdminForbiddenOnLANAndBrowserOrigin(t *testing.T) {
	s := startTest(t)
	req, _ := http.NewRequest("POST", s.lanURL+"/app/delete", strings.NewReader(`{"paths":["x"]}`))
	req.Header.Set("Authorization", "Bearer "+s.Token)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode < 400 {
		t.Fatal("LAN admin exposed")
	}
	req, _ = http.NewRequest("GET", s.AdminURL+"/app/peers", nil)
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Origin", "https://example.com")
	res, e = http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("browser origin allowed")
	}
}
func TestRealDufsDownloadAndClose(t *testing.T) {
	binary := os.Getenv("LOCALBUCKET_TEST_DUFS")
	if binary == "" {
		t.Skip("set LOCALBUCKET_TEST_DUFS for real bundled Dufs integration")
	}
	s, e := Start(Options{DataDir: t.TempDir(), DufsPath: binary, Port: -1, NoDiscovery: true})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.store.Receive(context.Background(), "hello.txt", strings.NewReader("from real dufs"))
	res, e := http.Get(s.lanURL + "/v1/file?path=hello.txt")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(b) != "from real dufs" {
		t.Fatalf("real dufs %q", b)
	}
	res, e = http.Get(s.lanURL + "/v1/list")
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	json.NewDecoder(res.Body).Decode(&v)
	res.Body.Close()
	if _, ok := v["files"]; !ok {
		t.Fatal(v)
	}
	dufsURL := s.dufsURL
	s.Close()
	client := &http.Client{Timeout: time.Second}
	if r, e := client.Get(dufsURL + "/__dufs__/health"); e == nil {
		r.Body.Close()
		t.Fatal("Dufs survived shutdown")
	}
}

func TestDiscoveryStartupShutdownBounded(t *testing.T) {
	if os.Getenv("LOCALBUCKET_TEST_DISCOVERY") == "" {
		t.Skip("opt-in actual multicast network test")
	}
	start := time.Now()
	s, e := Start(Options{DataDir: t.TempDir(), Port: -1})
	if e != nil {
		t.Fatal(e)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("discovery blocked startup")
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("discovery blocked shutdown")
	}
}

func TestDiscoveryBeaconFindsComputer(t *testing.T) {
	if os.Getenv("LOCALBUCKET_TEST_DISCOVERY") == "" {
		t.Skip("opt-in UDP network test")
	}
	a, e := Start(Options{DataDir: t.TempDir(), Port: -1})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b := startTest(t)
	packet, _ := json.Marshal(beacon{Magic: "LocalBucket/1", Kind: "announce", ID: b.Self.ID, Port: b.lanLn.Addr().(*net.TCPAddr).Port})
	conn, e := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: a.lanLn.Addr().(*net.TCPAddr).Port})
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	conn.Write(packet)
	until := time.Now().Add(2 * time.Second)
	for time.Now().Before(until) {
		ps := a.allPeers()
		if len(ps) == 2 {
			if ps[1].ID != b.Self.ID || ps[1].Local {
				t.Fatal(ps)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("announced peer was not automatically discovered")
}
