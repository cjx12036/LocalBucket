//go:build windows

// Disposable Win10 native drag/drop capability probe, not production code.
package main

import (
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

//go:embed dufs.exe
var dufs []byte

//go:embed vcruntime140.dll
var vcRuntime []byte

var user = syscall.NewLazyDLL("user32.dll")
var kernel = syscall.NewLazyDLL("kernel32.dll")
var shell = syscall.NewLazyDLL("shell32.dll")
var ole = syscall.NewLazyDLL("ole32.dll")
var createWindow = user.NewProc("CreateWindowExW")
var sendMessage = user.NewProc("SendMessageW")
var defWindow = user.NewProc("DefWindowProcW")
var globalAlloc = kernel.NewProc("GlobalAlloc")
var globalLock = kernel.NewProc("GlobalLock")
var globalUnlock = kernel.NewProc("GlobalUnlock")
var root, bucket, cache string
var window, list, status, address uintptr
var child *exec.Cmd
var entries []entry
var remoteBase string
var mouseX, mouseY int
var armed bool
var oldListProc uintptr
var keepAlive []any
var logMu sync.Mutex
var client = &http.Client{Timeout: 25 * time.Second}

type entry struct{ Name, Path, URL string }
type point struct{ X, Y int32 }
type msg struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}
type wndclass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Class                        *uint16
	SmallIcon                          uintptr
}

func ptr(s string) uintptr { p, _ := syscall.UTF16PtrFromString(s); return uintptr(unsafe.Pointer(p)) }
func call(d *syscall.LazyDLL, n string, a ...uintptr) uintptr {
	v, _, _ := d.NewProc(n).Call(a...)
	return v
}
func text(h uintptr, s string) { call(user, "SetWindowTextW", h, ptr(s)) }
func note(s string) {
	logMu.Lock()
	defer logMu.Unlock()
	f, e := os.OpenFile(filepath.Join(root, "events.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e == nil {
		fmt.Fprintln(f, time.Now().Format(time.RFC3339), s)
		f.Close()
	}
}
func report(s string) { note(s); text(status, s) }
func control(kind, label string, style uintptr, x, y, w, h, id int) uintptr {
	v, _, _ := createWindow.Call(0, ptr(kind), ptr(label), 0x50000000|style, uintptr(x), uintptr(y), uintptr(w), uintptr(h), window, uintptr(id), 0, 0)
	return v
}
func unique(dir, name string) string {
	p := filepath.Join(dir, filepath.Base(name))
	for i := 1; ; i++ {
		if _, e := os.Stat(p); os.IsNotExist(e) {
			return p
		}
		ext := filepath.Ext(name)
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(filepath.Base(name), ext), i, ext))
	}
}
func copyItem(src, dst string) error {
	st, e := os.Lstat(src)
	if e != nil {
		return e
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlink skipped")
	}
	if st.IsDir() {
		if e = os.Mkdir(dst, 0700); e != nil {
			return e
		}
		cs, e := os.ReadDir(src)
		if e != nil {
			return e
		}
		for _, c := range cs {
			if e = copyItem(filepath.Join(src, c.Name()), filepath.Join(dst, c.Name())); e != nil {
				return e
			}
		}
		return nil
	}
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	ce := out.Close()
	if e != nil {
		return e
	}
	return ce
}
func refreshLocal() {
	remoteBase = ""
	entries = nil
	cs, e := os.ReadDir(bucket)
	if e != nil {
		report(e.Error())
		return
	}
	for _, c := range cs {
		entries = append(entries, entry{Name: c.Name(), Path: filepath.Join(bucket, c.Name())})
	}
	renderList()
	report("LOCAL mode: drop files/folders in; drag a row out (COPY only).")
}
func renderList() {
	sendMessage.Call(list, 0x184, 0, 0)
	for _, e := range entries {
		prefix := "LOCAL   "
		if e.URL != "" {
			prefix = "REMOTE  "
		}
		sendMessage.Call(list, 0x180, 0, ptr(prefix+e.Name))
	}
}
func loadRemote() {
	b := make([]uint16, 2048)
	call(user, "GetWindowTextW", address, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	base := syscall.UTF16ToString(b)
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		report("Enter http://IP:47931/")
		return
	}
	u.RawQuery = "json"
	res, e := client.Get(u.String())
	if e != nil {
		report(e.Error())
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		report(res.Status)
		return
	}
	var d struct {
		Paths []struct {
			Name string `json:"name"`
			Type string `json:"path_type"`
		} `json:"paths"`
	}
	if e = json.NewDecoder(res.Body).Decode(&d); e != nil {
		report(e.Error())
		return
	}
	entries = nil
	for _, p := range d.Paths {
		if p.Type == "File" {
			f := *u
			f.RawQuery = ""
			f.Path = strings.TrimRight(u.Path, "/") + "/" + p.Name
			entries = append(entries, entry{Name: p.Name, URL: f.String()})
		}
	}
	remoteBase = base
	renderList()
	report("REMOTE mode: drag a row out; HTTP download is materialized into cache on shell request.")
}
func importDrop(drop uintptr) {
	defer call(shell, "DragFinish", drop)
	n := call(shell, "DragQueryFileW", drop, 0xffffffff, 0, 0)
	for i := uintptr(0); i < n; i++ {
		b := make([]uint16, 32768)
		call(shell, "DragQueryFileW", drop, i, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
		src := syscall.UTF16ToString(b)
		if remoteBase == "" {
			dst := unique(bucket, filepath.Base(src))
			e := copyItem(src, dst)
			note(fmt.Sprintf("DROP_IN src=%s dst=%s error=%v", src, dst, e))
			if e != nil {
				report(e.Error())
			}
		} else {
			f, e := os.Open(src)
			if e != nil {
				report(e.Error())
				continue
			}
			st, e := f.Stat()
			if e != nil || st.IsDir() {
				f.Close()
				report("Remote directory upload is outside this spike; test regular files.")
				continue
			}
			u, _ := url.Parse(remoteBase)
			u.Path = strings.TrimRight(u.Path, "/") + "/" + filepath.Base(src)
			req, e := http.NewRequest("PUT", u.String(), f)
			if e != nil {
				f.Close()
				report(e.Error())
				continue
			}
			req.ContentLength = st.Size()
			r, e := client.Do(req)
			f.Close()
			if e != nil {
				report(e.Error())
				continue
			}
			r.Body.Close()
			report("UPLOAD " + filepath.Base(src) + " " + r.Status)
		}
	}
	if remoteBase == "" {
		refreshLocal()
	} else {
		loadRemote()
	}
}

// Minimal OLE IDataObject/IDropSource + CF_HDROP. Always exposes COPY, never MOVE.
type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

var iidUnknown = guid{0, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
var iidData = guid{0x10e, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
var iidSource = guid{0x121, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
var iidEnum = guid{0x103, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

type format struct {
	CF     uint16
	Pad    [6]byte
	Target uintptr
	Aspect uint32
	Index  int32
	Tymed  uint32
	Pad2   uint32
}
type medium struct {
	Tymed           uint32
	Pad             uint32
	Handle, Release uintptr
}
type comObj struct {
	VTable *uintptr
	Kind   int
	Pos    int
	Entry  entry
	Cached string
}

func hr(x uint32) uintptr { return uintptr(x) }
func object(kind int, vt []uintptr) *comObj {
	o := &comObj{VTable: &vt[0], Kind: kind}
	keepAlive = append(keepAlive, o, vt)
	return o
}
func query(this, iid, out uintptr) uintptr {
	o := (*comObj)(unsafe.Pointer(this))
	g := *(*guid)(unsafe.Pointer(iid))
	wanted := iidData
	if o.Kind == 1 {
		wanted = iidSource
	} else if o.Kind == 2 {
		wanted = iidEnum
	}
	if g == iidUnknown || g == wanted {
		*(*uintptr)(unsafe.Pointer(out)) = this
		return 0
	}
	*(*uintptr)(unsafe.Pointer(out)) = 0
	return hr(0x80004002)
}
func addRef(this uintptr) uintptr  { return 2 }
func release(this uintptr) uintptr { return 1 }
func queryData(this, f uintptr) uintptr {
	v := (*format)(unsafe.Pointer(f))
	if v.CF == 15 && v.Aspect == 1 && v.Tymed&1 != 0 {
		return 0
	}
	return hr(0x80040064)
}
func dataGet(this, f, m uintptr) uintptr {
	if queryData(this, f) != 0 {
		return hr(0x80040064)
	}
	o := (*comObj)(unsafe.Pointer(this))
	path := o.Entry.Path
	if o.Entry.URL != "" {
		if o.Cached == "" {
			note("REMOTE_FETCH_BEGIN " + o.Entry.URL)
			res, e := client.Get(o.Entry.URL)
			if e != nil {
				return hr(0x80004005)
			}
			defer res.Body.Close()
			if res.StatusCode != 200 {
				return hr(0x80004005)
			}
			dir, e := os.MkdirTemp(cache, "drag-")
			if e != nil {
				return hr(0x80004005)
			}
			p := filepath.Join(dir, filepath.Base(o.Entry.Name))
			out, e := os.Create(p)
			if e != nil {
				return hr(0x80004005)
			}
			_, e = io.Copy(out, res.Body)
			ce := out.Close()
			if e != nil || ce != nil {
				return hr(0x80004005)
			}
			o.Cached = p
			note("REMOTE_FETCH_OK " + p)
		}
		path = o.Cached
	}
	names, _ := syscall.UTF16FromString(path)
	names = append(names, 0)
	size := 20 + len(names)*2
	h, _, _ := globalAlloc.Call(0x42, uintptr(size))
	if h == 0 {
		return hr(0x8007000e)
	}
	p, _, _ := globalLock.Call(h)
	if p == 0 {
		return hr(0x80004005)
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(p)), size)
	binary.LittleEndian.PutUint32(buf[0:4], 20)
	binary.LittleEndian.PutUint32(buf[16:20], 1)
	for i, n := range names {
		binary.LittleEndian.PutUint16(buf[20+i*2:], n)
	}
	globalUnlock.Call(h)
	*(*medium)(unsafe.Pointer(m)) = medium{Tymed: 1, Handle: h}
	return 0
}
func notImpl3(a, b, c uintptr) uintptr       { return hr(0x80004001) }
func notImpl2(a, b uintptr) uintptr          { return hr(0x80004001) }
func notImpl4(a, b, c, d uintptr) uintptr    { return hr(0x80004001) }
func notImpl5(a, b, c, d, e uintptr) uintptr { return hr(0x80004001) }
func canonical(a, b, c uintptr) uintptr {
	if c != 0 {
		(*format)(unsafe.Pointer(c)).Target = 0
	}
	return hr(0x80004001)
}
func enumFormats(this, direction, out uintptr) uintptr {
	if direction != 1 {
		return hr(0x80004001)
	}
	o := object(2, enumVT)
	*(*uintptr)(unsafe.Pointer(out)) = uintptr(unsafe.Pointer(o))
	return 0
}
func enumNext(this, count, formats, fetched uintptr) uintptr {
	o := (*comObj)(unsafe.Pointer(this))
	n := uint32(0)
	if o.Pos == 0 && count > 0 {
		*(*format)(unsafe.Pointer(formats)) = format{CF: 15, Aspect: 1, Index: -1, Tymed: 1}
		o.Pos = 1
		n = 1
	}
	if fetched != 0 {
		*(*uint32)(unsafe.Pointer(fetched)) = n
	}
	if uintptr(n) == count {
		return 0
	}
	return 1
}
func enumSkip(this, count uintptr) uintptr {
	o := (*comObj)(unsafe.Pointer(this))
	if count == 0 {
		return 0
	}
	if o.Pos == 0 && count == 1 {
		o.Pos = 1
		return 0
	}
	o.Pos = 1
	return 1
}
func enumReset(this uintptr) uintptr { (*comObj)(unsafe.Pointer(this)).Pos = 0; return 0 }
func enumClone(this, out uintptr) uintptr {
	o := object(2, enumVT)
	o.Pos = (*comObj)(unsafe.Pointer(this)).Pos
	*(*uintptr)(unsafe.Pointer(out)) = uintptr(unsafe.Pointer(o))
	return 0
}
func continueDrag(this, escape, keys uintptr) uintptr {
	if escape != 0 {
		return 0x40101
	}
	if keys&1 == 0 {
		return 0x40100
	}
	return 0
}
func feedback(this, effect uintptr) uintptr { return 0x40102 }

var dataVT, sourceVT, enumVT []uintptr

func initOLE() {
	cb := syscall.NewCallback
	dataVT = []uintptr{cb(query), cb(addRef), cb(release), cb(dataGet), cb(notImpl3), cb(queryData), cb(canonical), cb(notImpl4), cb(enumFormats), cb(notImpl5), cb(notImpl2), cb(notImpl3)}
	sourceVT = []uintptr{cb(query), cb(addRef), cb(release), cb(continueDrag), cb(feedback)}
	enumVT = []uintptr{cb(query), cb(addRef), cb(release), cb(enumNext), cb(enumSkip), cb(enumReset), cb(enumClone)}
}
func dragOut(i int) {
	if i < 0 || i >= len(entries) {
		return
	}
	o := object(0, dataVT)
	o.Entry = entries[i]
	s := object(1, sourceVT)
	note("DRAG_BEGIN " + o.Entry.Name)
	var effect uint32
	r := call(ole, "DoDragDrop", uintptr(unsafe.Pointer(o)), uintptr(unsafe.Pointer(s)), 1, uintptr(unsafe.Pointer(&effect)))
	report(fmt.Sprintf("DRAG_END HRESULT=0x%x effect=%d (1=COPY). Source retained.", r, effect))
	runtime.KeepAlive(o)
	runtime.KeepAlive(s)
}
func listProc(h uintptr, m uint32, w, l uintptr) uintptr {
	if m == 0x201 {
		mouseX = int(int16(l & 0xffff))
		mouseY = int(int16((l >> 16) & 0xffff))
		armed = true
	}
	if m == 0x202 {
		armed = false
	}
	if m == 0x200 && armed && w&1 != 0 {
		x := int(int16(l & 0xffff))
		y := int(int16((l >> 16) & 0xffff))
		if abs(x-mouseX)+abs(y-mouseY) > 8 {
			armed = false
			i, _, _ := sendMessage.Call(list, 0x188, 0, 0)
			dragOut(int(int32(i)))
			return 0
		}
	}
	if m == 0x233 {
		importDrop(w)
		return 0
	}
	return call(user, "CallWindowProcW", oldListProc, h, uintptr(m), w, l)
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
func wndProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x233:
		importDrop(w)
		return 0
	case 0x111:
		switch w & 0xffff {
		case 101:
			refreshLocal()
		case 102:
			loadRemote()
		case 103:
			call(shell, "ShellExecuteW", 0, ptr("open"), ptr(bucket), 0, 0, 1)
		}
		return 0
	case 0x2:
		call(user, "PostQuitMessage", 0)
		return 0
	}
	return call(user, "DefWindowProcW", h, uintptr(m), w, l)
}
func main() {
	runtime.LockOSThread()
	root = filepath.Join(os.Getenv("LOCALAPPDATA"), "LocalBucketDragSpike")
	bucket = filepath.Join(root, "bucket")
	cache = filepath.Join(root, "cache")
	os.MkdirAll(bucket, 0700)
	os.MkdirAll(cache, 0700)
	fixture := filepath.Join(bucket, "Windows 拖出测试.txt")
	if _, e := os.Stat(fixture); os.IsNotExist(e) {
		os.WriteFile(fixture, []byte("Windows native COPY test; original stays in bucket.\r\n"), 0600)
	}
	binaryPath := filepath.Join(root, "dufs-v0.47.0.exe")
	if e := os.WriteFile(filepath.Join(root, "vcruntime140.dll"), vcRuntime, 0600); e != nil {
		call(user, "MessageBoxW", 0, ptr(e.Error()), ptr("Runtime extraction error"), 0)
		return
	}
	if e := os.WriteFile(binaryPath, dufs, 0700); e != nil {
		call(user, "MessageBoxW", 0, ptr(e.Error()), ptr("Startup error"), 0)
		return
	}
	child = exec.Command(binaryPath, bucket, "-b", "0.0.0.0", "-p", "47931", "--allow-upload", "--allow-archive")
	child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if e := child.Start(); e != nil {
		note("DUFS_START_ERROR " + e.Error())
	}
	defer func() {
		if child.Process != nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	call(ole, "OleInitialize", 0)
	defer call(ole, "OleUninitialize")
	initOLE()
	instance := call(kernel, "GetModuleHandleW", 0)
	class, _ := syscall.UTF16PtrFromString("LocalBucketDragSpike")
	wc := wndclass{Size: uint32(unsafe.Sizeof(wndclass{})), Proc: syscall.NewCallback(wndProc), Instance: instance, Class: class, Background: 6}
	call(user, "RegisterClassExW", uintptr(unsafe.Pointer(&wc)))
	window, _, _ = createWindow.Call(0, uintptr(unsafe.Pointer(class)), ptr("LocalBucket Drag Spike - disposable prototype"), 0x10cf0000, 80, 80, 850, 600, 0, 0, instance, 0)
	control("STATIC", "Native Win10 drag/drop probe. Test files only. Dufs :47931", 0, 16, 12, 800, 22, 0)
	address = control("EDIT", "http://127.0.0.1:47931/", 0x00800080, 16, 42, 500, 28, 0)
	control("BUTTON", "Load remote", 0, 525, 42, 130, 28, 102)
	control("BUTTON", "Local", 0, 665, 42, 100, 28, 101)
	list = control("LISTBOX", "", 0x00a00001, 16, 84, 800, 350, 100)
	call(shell, "DragAcceptFiles", list, 1)
	call(shell, "DragAcceptFiles", window, 1)
	oldListProc = call(user, "SetWindowLongPtrW", list, ^uintptr(3), syscall.NewCallback(listProc))
	status = control("STATIC", "Ready", 0, 16, 446, 800, 64, 0)
	control("BUTTON", "Open test bucket", 0, 16, 510, 180, 28, 103)
	refreshLocal()
	note("START bucket=" + bucket)
	call(user, "ShowWindow", window, 5)
	call(user, "UpdateWindow", window)
	var m msg
	for {
		r := call(user, "GetMessageW", uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		call(user, "TranslateMessage", uintptr(unsafe.Pointer(&m)))
		call(user, "DispatchMessageW", uintptr(unsafe.Pointer(&m)))
	}
	note("STOP")
}
