package node

import (
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

func TestPortConflictReturnsErrorWithoutPanic(t *testing.T) {
	s := startTest(t)
	port := s.lanLn.Addr().(*net.TCPAddr).Port
	if other, e := Start(Options{DataDir: t.TempDir(), Port: port, NoDiscovery: true}); e == nil {
		other.Close()
		t.Fatal("expected occupied-port error")
	}
}

func startTest(t *testing.T) *Service {
	t.Helper()
	s, e := Start(Options{DataDir: t.TempDir(), Port: -1, NoDiscovery: true})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestServicePermissionsAndRoundTrip(t *testing.T) {
	s := startTest(t)
	base := s.lanURL
	res, e := http.Get(s.AdminURL + "/app/peers")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("admin without token allowed")
	}
	for _, method := range []string{"DELETE", "PUT"} {
		req, _ := http.NewRequest(method, base+"/v1/file?path=x", nil)
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode < 400 {
			t.Fatal("LAN destructive request accepted")
		}
	}
	for i := 0; i < 2; i++ {
		r, e := http.Post(base+"/v1/upload?path=hello.txt", "application/octet-stream", strings.NewReader("same bytes"))
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("upload %s %s", r.Status, b)
		}
	}
	r, e := http.Get(base + "/v1/file?path=hello.txt")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if string(b) != "same bytes" {
		t.Fatalf("download %q", b)
	}
	fs, _ := s.store.List("")
	if len(fs) != 1 {
		t.Fatal(fs)
	}
	for _, p := range []string{"..%2Fsecret", "link%2Fsecret"} {
		r, e := http.Post(base+"/v1/upload?path="+p, "text/plain", strings.NewReader("x"))
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if p == "..%2Fsecret" && r.StatusCode < 400 {
			t.Fatal("traversal allowed")
		}
	}
}
func TestServiceLocalJobsAndShutdown(t *testing.T) {
	s := startTest(t)
	src := filepath.Join(t.TempDir(), "中文.txt")
	os.WriteFile(src, []byte("data"), 0600)
	req, _ := http.NewRequest("POST", s.AdminURL+"/app/import", strings.NewReader(`{"peer":"`+s.Self.ID+`","path":"","sources":["`+src+`"]}`))
	req.Header.Set("Authorization", "Bearer "+s.Token)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]string
	json.NewDecoder(res.Body).Decode(&v)
	res.Body.Close()
	if v["job"] == "" {
		t.Fatal(v)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j := s.getJob(v["job"])
		if j.Status == "done" {
			break
		}
		if j.Status == "error" {
			t.Fatal(j.Error)
		}
		time.Sleep(time.Millisecond * 10)
	}
	if s.getJob(v["job"]).Status != "done" {
		t.Fatal("job timed out")
	}
	s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, _ = http.NewRequestWithContext(ctx, "GET", s.AdminURL+"/app/peers", nil)
	if r, e := http.DefaultClient.Do(req); e == nil {
		r.Body.Close()
		t.Fatal("listener survived close")
	}
}
