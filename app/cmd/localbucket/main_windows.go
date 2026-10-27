//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/cjx12036/LocalBucket/app/internal/node"
)

//go:embed dufs.exe
var dufs []byte

//go:embed vcruntime140.dll
var vcRuntime []byte

var user = syscall.NewLazyDLL("user32.dll")
var kernel = syscall.NewLazyDLL("kernel32.dll")
var shell = syscall.NewLazyDLL("shell32.dll")
var ole = syscall.NewLazyDLL("ole32.dll")
var comdlg = syscall.NewLazyDLL("comdlg32.dll")

func call(d *syscall.LazyDLL, n string, a ...uintptr) uintptr {
	v, _, _ := d.NewProc(n).Call(a...)
	return v
}

// Keep label arguments rooted until native calls and any nested OLE loop return.
var utf16Roots [][]uint16

func ptr(s string) uintptr {
	p, _ := syscall.UTF16FromString(s)
	if len(p) == 0 {
		p = []uint16{0}
	}
	utf16Roots = append(utf16Roots, p)
	return uintptr(unsafe.Pointer(&p[0]))
}
func text(h uintptr, s string) { call(user, "SetWindowTextW", h, ptr(s)) }

type point struct{ X, Y int32 }
type rect struct{ L, T, R, B int32 }
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
type peer struct {
	ID, Name, Address string
	Local, Online     bool
}
type job struct {
	ID, Kind, Status, Message, Error string
	Completed, Total                 int64
	Paths                            []string
	Result                           struct{ Added, Skipped, Renamed int }
}

var service *node.Service
var window, list, breadcrumb, status, jobsBox, filter, emptyLabel, font, heading, destination, columns uintptr
var unavailableCount int
var columnLabels [4]uintptr
var controls = map[int]uintptr{}
var peers []peer
var files []file
var shown []int
var renderedKeys []string
var selectedPeer *peer
var currentPath string
var generation uint64
var jobs []job
var jobSeen = map[string]string{}

type retryRequest struct {
	Endpoint string
	Data     any
}

var retryRequests = map[string]retryRequest{}
var lastFailed string
var preparing string
var prepared []string
var preparedKey string
var listingSnapshot, preparingSnapshot, preparedSnapshot string
var preparedAt time.Time
var preparedDirectory bool
var pendingSubmissions int
var submittedActive = map[string]bool{}
var loading, polling, closing bool
var closeChecking bool
var oldListProc uintptr
var mouseX, mouseY int
var armed bool
var dragPeer, dragPath string
var dragActive bool
var ctx, cancel = context.WithCancel(context.Background())
var httpClient = &http.Client{Timeout: 30 * time.Second}
var uiQueue = make(chan func(), 256)
var postMu sync.Mutex

const uiMessage = 0x8001
const className = "LocalBucketProductWindow"

func post(fn func()) {
	postMu.Lock()
	defer postMu.Unlock()
	select {
	case <-ctx.Done():
		return
	case uiQueue <- fn:
		call(user, "PostMessageW", window, uiMessage, 0, 0)
	}
}
func report(s string) { text(status, s) }
func api(method, endpoint string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, service.AdminURL+endpoint, body)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+service.Token)
	req.Header.Set("Content-Type", "application/json")
	res, e := httpClient.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var d struct{ Error string }
		json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&d)
		if d.Error != "" {
			return fmt.Errorf("%s", d.Error)
		}
		return fmt.Errorf("请求失败：%s", res.Status)
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
	}
	return nil
}
func action(endpoint string, data any) {
	if closing {
		return
	}
	if endpoint == "/app/prepare" {
		preparing = "starting"
		preparingSnapshot = listingSnapshot
	}
	pendingSubmissions++
	go func() {
		var r struct{ Job string }
		e := api("POST", endpoint, data, &r)
		post(func() {
			pendingSubmissions--
			if closing {
				return
			}
			if e != nil {
				if endpoint == "/app/prepare" {
					preparing = ""
					invalidatePrepared()
				}
				report(e.Error())
				return
			}
			retryRequests[r.Job] = retryRequest{Endpoint: endpoint, Data: data}
			if r.Job != "" {
				submittedActive[r.Job] = true
			}
			if endpoint == "/app/prepare" {
				preparing = r.Job
			} else {
				report("任务已开始，可在下方查看进度")
			}
			poll()
			if endpoint != "/app/prepare" {
				refresh()
			}
		})
	}()
}

func invalidatePrepared() {
	prepared = nil
	preparedKey = ""
	preparedSnapshot = ""
	preparedAt = time.Time{}
}
func preparedFresh() bool {
	ttl := 30 * time.Second
	if preparedDirectory {
		ttl = 10 * time.Second
	}
	return len(prepared) > 0 && !preparedAt.IsZero() && time.Since(preparedAt) < ttl && preparedSnapshot == listingSnapshot
}
func control(kind, label string, style uintptr, id int) uintptr {
	h := call(user, "CreateWindowExW", 0, ptr(kind), ptr(label), 0x50000000|style, 0, 0, 100, 30, window, uintptr(id), 0, 0)
	call(user, "SendMessageW", h, 0x30, font, 1)
	if id != 0 {
		controls[id] = h
	}
	return h
}
func setUIFont(dpi uint32) {
	if dpi == 0 {
		dpi = 96
	}
	height := uintptr(16 * dpi / 96)
	next := call(syscall.NewLazyDLL("gdi32.dll"), "CreateFontW", ^(height - 1), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, ptr("Microsoft YaHei UI"))
	if next == 0 {
		return
	}
	old := font
	font = next
	for _, h := range controls {
		call(user, "SendMessageW", h, 0x30, font, 1)
	}
	for _, h := range []uintptr{breadcrumb, status, jobsBox, emptyLabel, heading, destination, columns, columnLabels[0], columnLabels[1], columnLabels[2], columnLabels[3]} {
		if h != 0 {
			call(user, "SendMessageW", h, 0x30, font, 1)
		}
	}
	if old != 0 {
		call(syscall.NewLazyDLL("gdi32.dll"), "DeleteObject", old)
	}
}
func move(h uintptr, x, y, w, hgt int) {
	if w < 10 {
		w = 10
	}
	if hgt < 10 {
		hgt = 10
	}
	call(user, "MoveWindow", h, uintptr(x), uintptr(y), uintptr(w), uintptr(hgt), 1)
}
func layout() {
	var r rect
	call(user, "GetClientRect", window, uintptr(unsafe.Pointer(&r)))
	w, h := int(r.R), int(r.B)
	dpi := call(user, "GetDpiForWindow", window)
	if dpi == 0 {
		dpi = 96
	}
	s := func(v int) int { return v * int(dpi) / 96 }
	m := s(24)
	move(heading, m, s(20), w-2*m-s(330), s(30))
	move(controls[103], w-m-s(304), s(18), s(110), s(34))
	move(controls[105], w-m-s(184), s(18), s(110), s(34))
	move(controls[113], w-m-s(64), s(18), s(64), s(34))
	move(controls[101], m, s(72), s(50), s(30))
	move(controls[102], m+s(56), s(72), s(82), s(30))
	move(breadcrumb, m+s(150), s(77), w-2*m-s(430), s(26))
	move(filter, w-m-s(260), s(72), s(260), s(30))
	move(destination, m, s(114), w-2*m, s(24))
	cw := w - 2*m - s(30)
	starts := []int{0, cw * 39 / 100, cw * 51 / 100, cw * 76 / 100, cw}
	for i, label := range columnLabels {
		move(label, m+s(12)+starts[i], s(151), starts[i+1]-starts[i], s(24))
	}
	move(list, m, s(179), w-2*m, h-s(292))
	move(emptyLabel, m+s(24), s(228), w-2*m-s(48), s(64))
	move(status, m, h-s(99), w-2*m-s(190), s(28))
	move(controls[111], w-m-s(180), h-s(100), s(85), s(27))
	move(controls[112], w-m-s(85), h-s(100), s(85), s(27))
	move(jobsBox, m, h-s(64), w-2*m, s(42))
	call(user, "SendMessageW", list, 0x1a0, 0, uintptr(s(38)))
}
func render() {
	top := call(user, "SendMessageW", list, 0x18e, 0, 0)
	selected := map[string]bool{}
	for i, key := range renderedKeys {
		if call(user, "SendMessageW", list, 0x187, uintptr(i), 0) == 1 {
			selected[key] = true
		}
	}
	renderedKeys = nil
	shown = nil
	call(user, "SendMessageW", list, 0xB, 0, 0)
	call(user, "SendMessageW", list, 0x184, 0, 0)
	var b [1024]uint16
	call(user, "GetWindowTextW", filter, uintptr(unsafe.Pointer(&b[0])), 1024)
	q := strings.ToLower(syscall.UTF16ToString(b[:]))
	for i, f := range files {
		if q != "" && !strings.Contains(strings.ToLower(f.Name+" "+f.Owner), q) {
			continue
		}
		name, size := f.Name, formatSize(f.Size)
		if f.IsDir {
			name = "▸ " + name
			size = "文件夹"
		}
		owner := f.Owner
		if f.Local {
			owner += " · 本机"
		}
		label := fmt.Sprintf("%s\t%s\t%s\t%s", name, size, owner, time.Unix(f.Modified, 0).Local().Format("2006/01/02 15:04"))
		call(user, "SendMessageW", list, 0x180, 0, ptr(label))
		shown = append(shown, i)
		renderedKeys = append(renderedKeys, fileKey(f))
	}
	online := 0
	for _, p := range peers {
		if p.Online {
			online++
		}
	}
	title := fmt.Sprintf("共享空间  ·  %d 台在线", online)
	if unavailableCount > 0 {
		title += fmt.Sprintf(" · %d 台暂不可用", unavailableCount)
	}
	text(heading, title)
	crumb := "所有文件"
	dest := "添加到本机存储桶 · 文件保留在来源电脑，在线时可用"
	if selectedPeer != nil {
		crumb = selectedPeer.Name + "  >  " + strings.ReplaceAll(currentPath, "/", "  >  ")
		dest = "添加到「" + selectedPeer.Name + " / " + currentPath + "」"
	}
	text(breadcrumb, crumb)
	text(destination, dest)
	text(emptyLabel, "把文件或文件夹拖到这里\r\n点击“添加…”开始共享；附近电脑上的文件会自动出现。")
	for i, key := range renderedKeys {
		if selected[key] {
			call(user, "SendMessageW", list, 0x185, 1, uintptr(i))
		}
	}
	if top < uintptr(len(shown)) {
		call(user, "SendMessageW", list, 0x197, top, 0)
	}
	visible := uintptr(0)
	if len(shown) == 0 {
		visible = 5
	}
	call(user, "ShowWindow", emptyLabel, visible)
	updateSelectionActions()
	call(user, "EnableWindow", controls[101], map[bool]uintptr{true: 1, false: 0}[selectedPeer != nil])
	call(user, "SendMessageW", list, 0xB, 1, 0)
	call(user, "InvalidateRect", list, 0, 1)
}
func updateSelectionActions() {
	enabled := uintptr(0)
	if len(chosen()) > 0 {
		enabled = 1
	}
	call(user, "EnableWindow", controls[105], enabled)
}
func destinationPeer() string {
	if selectedPeer != nil {
		return selectedPeer.ID
	}
	return ""
}
func sameDragDestination() bool {
	return dragActive && (selectedPeer == nil || (dragPeer == destinationPeer() && dragPath == currentPath))
}
func formatSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1<<20 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	if n < 1<<30 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}
func refresh() {
	if loading || closing {
		return
	}
	loading = true
	g := generation
	id, rel := destinationPeer(), currentPath
	var owner peer
	if selectedPeer != nil {
		owner = *selectedPeer
	}
	go func() {
		var ps struct{ Peers []peer }
		var fs struct {
			Files       []file
			Peers       []peer
			Unavailable []struct{ Peer, Owner, Error string }
		}
		var e error
		if id == "" {
			e = api("GET", "/app/space", nil, &fs)
			ps.Peers = fs.Peers
		} else {
			e = api("GET", "/app/peers", nil, &ps)
			if e == nil {
				e = api("GET", "/app/list?peer="+url.QueryEscape(id)+"&path="+url.QueryEscape(rel), nil, &fs)
			}
			for i := range fs.Files {
				fs.Files[i].Peer = id
				fs.Files[i].Owner = owner.Name
				fs.Files[i].Local = owner.Local
			}
		}
		post(func() {
			loading = false
			if closing {
				return
			}
			if g != generation {
				refresh()
				return
			}
			if e != nil {
				report(e.Error())
				return
			}
			peers = ps.Peers
			files = fs.Files
			unavailableCount = len(fs.Unavailable)
			listingSnapshot = metadataFingerprint(id, rel, files)
			if len(prepared) > 0 && preparedSnapshot != listingSnapshot {
				invalidatePrepared()
				report("文件列表已变化，请重新拖动以准备最新文件")
			}
			render()
		})
	}()
}
func poll() {
	if polling || closing {
		return
	}
	polling = true
	go func() {
		var r struct{ Jobs []job }
		e := api("GET", "/app/jobs", nil, &r)
		post(func() {
			polling = false
			if closing {
				return
			}
			if e != nil {
				report(e.Error())
				return
			}
			jobs = r.Jobs
			var lines []string
			refreshNeeded := false
			for _, j := range jobs {
				if j.Status != "running" {
					delete(submittedActive, j.ID)
				}
				line := j.Message
				if j.Total > 0 {
					line += fmt.Sprintf("  %s / %s", formatSize(j.Completed), formatSize(j.Total))
				}
				if j.Error != "" {
					line += "  " + j.Error
				}
				if line == "" {
					line = j.Kind + " · " + j.Status
				}
				lines = append(lines, line)
				if jobSeen[j.ID] != j.Status {
					jobSeen[j.ID] = j.Status
					if j.Status == "error" {
						lastFailed = j.ID
					}
					if j.Status == "done" || j.Status == "error" || j.Status == "canceled" {
						report(line)
						refreshNeeded = true
					}
				}
				if j.ID == preparing {
					if j.Status == "done" {
						preparing = ""
						if preparingSnapshot != listingSnapshot {
							invalidatePrepared()
							report("源文件信息已变化，请重新拖动以准备最新文件")
						} else {
							prepared = j.Paths
							preparedSnapshot = preparingSnapshot
							preparedAt = time.Now()
							report("文件已准备好，请再次拖动所选文件；拖出始终复制，原文件保留。")
						}
					} else if j.Status == "error" || j.Status == "canceled" {
						preparing = ""
						preparedKey = ""
					}
				}
			}
			if len(lines) > 2 {
				lines = lines[len(lines)-2:]
			}
			text(jobsBox, strings.Join(lines, "\r\n"))
			if refreshNeeded {
				refresh()
			}
		})
	}()
}
func chosen() []file {
	n := int(int32(call(user, "SendMessageW", list, 0x190, 0, 0)))
	if n <= 0 {
		return nil
	}
	idx := make([]int32, n)
	call(user, "SendMessageW", list, 0x191, uintptr(n), uintptr(unsafe.Pointer(&idx[0])))
	var out []file
	for _, i := range idx {
		if i >= 0 && int(i) < len(shown) {
			out = append(out, files[shown[i]])
		}
	}
	return out
}
func selectedPaths() []string {
	var p []string
	for _, f := range chosen() {
		p = append(p, f.Path)
	}
	return p
}
func importSources(sources []string) {
	if sameDragDestination() {
		report("文件已在共享空间，无需重复添加")
		return
	}
	if len(sources) > 0 {
		action("/app/import", map[string]any{"peer": destinationPeer(), "path": currentPath, "sources": sources})
	}
}
func openSelected() {
	f := chosen()
	if len(f) != 1 || !f[0].IsDir {
		return
	}
	selectedPeer = &peer{ID: f[0].Peer, Name: f[0].Owner, Local: f[0].Local, Online: true}
	currentPath = f[0].Path
	navigate()
}
func navigate() {
	generation++
	files = nil
	invalidatePrepared()
	renderedKeys = nil
	text(filter, "")
	render()
	refresh()
}
func up() {
	if selectedPeer == nil {
		return
	}
	currentPath = path.Dir(currentPath)
	if currentPath == "." || currentPath == "" {
		selectedPeer = nil
		currentPath = ""
	}
	navigate()
}
func dragSelection() {
	if closing {
		return
	}
	selection := chosen()
	if len(selection) == 0 {
		return
	}
	directory := false
	for _, f := range selection {
		directory = directory || f.IsDir
	}
	key := metadataFingerprint(destinationPeer(), currentPath, selection)
	if allLocal(selection) {
		var originals []string
		for _, f := range selection {
			originals = append(originals, filepath.Join(service.BucketDir, filepath.FromSlash(f.Path)))
		}
		runDrag(originals)
		return
	}
	if preparedKey == key && preparedFresh() {
		if runDrag(prepared) {
			invalidatePrepared()
		}
		return
	}
	if preparing != "" {
		report("正在准备文件，请稍候；完成后请再次拖动")
		return
	}
	invalidatePrepared()
	preparedKey = key
	preparedDirectory = directory
	report("正在准备所选文件…完成后请再次拖动，也可使用“另存到…”")
	action("/app/prepare", map[string]any{"items": selectionItems(selection)})
}
func showHelp() {
	message("使用 LocalBucket", "共享空间显示所有在线电脑的文件，位置栏标明文件来源。双击文件夹打开。\r\n\r\n首页拖入或添加：复制到本机存储桶；文件夹内添加：复制到所属电脑。\r\n拖出或另存到：复制文件，原文件保留。\r\n远端文件首次拖出需先准备，完成后请再次拖动。\r\n只有本机文件可删除，删除后进入回收站。\r\n\r\n关闭窗口会停止共享并退出。附近电脑需运行软件并连接同一局域网。", 0)
	go func() {
		var d struct {
			Message   string
			Port      int
			Addresses []string
			Discovery string
		}
		e := api("GET", "/app/diagnostics", nil, &d)
		post(func() {
			if e != nil {
				report(e.Error())
				return
			}
			message("网络诊断", fmt.Sprintf("%s\r\n%s\r\n服务端口：%d\r\n本机地址：%s\r\n\r\n如未发现电脑，请检查系统网络授权、防火墙和路由器客户端隔离设置。", d.Message, d.Discovery, d.Port, strings.Join(d.Addresses, "、")), 0)
		})
	}()
}
func message(title, s string, flags uintptr) uintptr {
	return call(user, "MessageBoxW", window, ptr(s), ptr(title), flags)
}
func onboarding() {
	go func() {
		var c struct {
			LegacyAvailable bool `json:"legacy_available"`
			GuideSeen       bool `json:"guide_seen"`
		}
		e := api("GET", "/app/config", nil, &c)
		post(func() {
			if e != nil {
				report(e.Error())
				return
			}
			if !c.GuideSeen {
				message("欢迎使用 LocalBucket", "共享空间直接显示所有在线电脑的文件。位置栏标明来源。\r\n拖入文件进行存放，拖出文件复制到桌面。\r\n附近电脑会自动出现，无需输入地址。\r\n关闭窗口即停止共享并退出。", 0)
				go func() {
					if e := api("POST", "/app/config", map[string]bool{"guide_seen": true}, nil); e != nil {
						post(func() { report(e.Error()) })
					}
				}()
			}
			if c.LegacyAvailable && message("导入旧测试文件", "发现旧版测试存储桶。是否复制到本机新存储桶？原有文件将保留。", 0x24) == 6 {
				action("/app/migrate", map[string]any{})
			}
		})
	}()
}
func closeApp() {
	if closing || closeChecking {
		return
	}
	closeChecking = true
	go func() {
		var r struct{ Jobs []job }
		e := api("GET", "/app/jobs", nil, &r)
		post(func() {
			closeChecking = false
			if e != nil {
				if message("退出 LocalBucket", "无法确认任务状态。退出会停止共享并中断尚未完成的传输，是否退出？", 0x24) != 6 {
					return
				}
			} else {
				running := pendingSubmissions > 0 || preparing != "" || len(submittedActive) > 0
				for _, j := range r.Jobs {
					running = running || j.Status == "running"
				}
				if running && message("退出 LocalBucket", "仍有任务正在进行。退出会中断传输并停止共享，是否退出？", 0x24) != 6 {
					return
				}
			}
			closing = true
			report("正在停止共享并退出…")
			call(user, "EnableWindow", window, 0)
			go func() {
				for _, j := range r.Jobs {
					if j.Status == "running" {
						api("POST", "/app/cancel", map[string]string{"job": j.ID}, nil)
					}
				}
				service.Close()
				post(func() {
					call(ole, "RevokeDragDrop", list)
					call(ole, "RevokeDragDrop", window)
					call(user, "DestroyWindow", window)
				})
			}()
		})
	}()
}
func showMenu(entries []struct {
	id       int
	label    string
	disabled bool
}, anchor uintptr) {
	menu := call(user, "CreatePopupMenu")
	defer call(user, "DestroyMenu", menu)
	for _, e := range entries {
		flags := uintptr(0)
		if e.disabled {
			flags = 3
		}
		call(user, "AppendMenuW", menu, flags, uintptr(e.id), ptr(e.label))
	}
	var r rect
	call(user, "GetWindowRect", anchor, uintptr(unsafe.Pointer(&r)))
	cmd := call(user, "TrackPopupMenu", menu, 0x100, uintptr(r.L), uintptr(r.B), 0, window, 0)
	if cmd != 0 {
		call(user, "PostMessageW", window, 0x111, cmd, 0)
	}
}
func showAddMenu() {
	showMenu([]struct {
		id       int
		label    string
		disabled bool
	}{{114, "添加文件…", false}, {104, "添加文件夹…", false}}, controls[103])
}
func showMoreMenu() {
	showMenu([]struct {
		id       int
		label    string
		disabled bool
	}{{110, "刷新", false}, {107, "打开本机存储位置", false}, {106, "移到回收站（仅本机）", !allLocal(chosen())}, {108, "使用说明与网络诊断", false}}, controls[113])
}
func drawFileRow(lp uintptr) {
	d := (*struct {
		Type, Control, Item, Action, State uint32
		Window, DC                         uintptr
		Bounds                             rect
		Data                               uintptr
	})(unsafe.Pointer(lp))
	if d.Control != 100 || int(d.Item) >= len(shown) {
		return
	}
	f := files[shown[d.Item]]
	selected := d.State&1 != 0
	bg, fg, sub := uintptr(0xffffff), uintptr(0x252525), uintptr(0x707070)
	if selected {
		bg = call(user, "GetSysColor", 13)
		fg = call(user, "GetSysColor", 14)
		sub = fg
	}
	gdi := syscall.NewLazyDLL("gdi32.dll")
	brush := call(gdi, "CreateSolidBrush", bg)
	call(user, "FillRect", d.DC, uintptr(unsafe.Pointer(&d.Bounds)), brush)
	call(gdi, "DeleteObject", brush)
	call(gdi, "SetBkMode", d.DC, 1)
	old := call(gdi, "SelectObject", d.DC, font)
	defer call(gdi, "SelectObject", d.DC, old)
	dpi := call(user, "GetDpiForWindow", window)
	if dpi == 0 {
		dpi = 96
	}
	inset := int32(12 * dpi / 96)
	r := d.Bounds
	r.L += inset
	r.R -= inset
	width := r.R - r.L
	starts := []int32{0, width * 39 / 100, width * 51 / 100, width * 76 / 100, width}
	name, size, owner := f.Name, formatSize(f.Size), f.Owner
	if f.IsDir {
		name = "▸ " + name
		size = "文件夹"
	}
	if f.Local {
		owner += " · 本机"
	}
	labels := []string{name, size, owner, time.Unix(f.Modified, 0).Local().Format("2006/01/02 15:04")}
	for i, label := range labels {
		cell := rect{r.L + starts[i], r.T, r.L + starts[i+1] - inset, r.B}
		color := sub
		if i == 0 {
			color = fg
		}
		call(gdi, "SetTextColor", d.DC, color)
		call(user, "DrawTextW", d.DC, ptr(label), ^uintptr(0), uintptr(unsafe.Pointer(&cell)), 0x8824)
	}
	if d.State&0x10 != 0 {
		call(user, "DrawFocusRect", d.DC, uintptr(unsafe.Pointer(&d.Bounds)))
	}
}
func wndProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x2b: // WM_DRAWITEM: roomy native rows, subdued source metadata.
		drawFileRow(l)
		return 1
	case uiMessage:
		for {
			select {
			case fn := <-uiQueue:
				fn()
			default:
				return 0
			}
		}
	case 0x5:
		layout()
		return 0
	case 0x2E0:
		setUIFont(uint32(w & 0xffff))
		r := (*rect)(unsafe.Pointer(l))
		call(user, "SetWindowPos", h, 0, uintptr(r.L), uintptr(r.T), uintptr(r.R-r.L), uintptr(r.B-r.T), 4)
		layout()
		return 0
	case 0x24: // minimum tracking size
		p := (*[10]int32)(unsafe.Pointer(l))
		dpi := call(user, "GetDpiForWindow", h)
		if dpi == 0 {
			dpi = 96
		}
		p[6] = int32(820 * dpi / 96)
		p[7] = int32(540 * dpi / 96)
		return 0
	case 0x113:
		if w == 1 {
			refresh()
		} else {
			poll()
		}
		return 0
	case 0x111:
		id := int(w & 0xffff)
		notification := (w >> 16) & 0xffff
		if id == 100 && notification == 1 {
			updateSelectionActions()
			return 0
		}
		if id == 100 && notification == 2 {
			openSelected()
			return 0
		}
		if id == 109 && notification == 0x300 {
			render()
			return 0
		}
		switch id {
		case 101:
			up()
		case 102:
			selectedPeer = nil
			currentPath = ""
			navigate()
		case 103:
			showAddMenu()
		case 114:
			importSources(pickFiles())
		case 113:
			showMoreMenu()
		case 104:
			if p := pickFolder("选择要添加的文件夹"); p != "" {
				importSources([]string{p})
			}
		case 105:
			items := selectionItems(chosen())
			if len(items) > 0 {
				if p := pickFolder("选择另存到的位置"); p != "" {
					action("/app/save", map[string]any{"items": items, "destination": p})
				}
			}
		case 106:
			if allLocal(chosen()) {
				ps := selectedPaths()
				if len(ps) > 0 && message("移到回收站", fmt.Sprintf("将选中的 %d 个项目移到回收站？", len(ps)), 0x24) == 6 {
					go func() {
						e := api("POST", "/app/delete", map[string]any{"paths": ps}, nil)
						post(func() {
							if e != nil {
								report(e.Error())
							} else {
								report("已移到回收站")
								refresh()
							}
						})
					}()
				}
			}
		case 107:
			call(shell, "ShellExecuteW", window, ptr("open"), ptr(service.BucketDir), 0, 0, 1)
		case 108:
			showHelp()
		case 111:
			if r, ok := retryRequests[lastFailed]; ok {
				if r.Endpoint == "/app/prepare" {
					if preparing == "" {
						invalidatePrepared()
						dragSelection()
					}
				} else {
					action(r.Endpoint, r.Data)
				}
			} else if preparing == "" && preparedKey != "" {
				preparedKey = ""
				dragSelection()
			} else {
				report("没有可重试的任务；也可重新选择文件进行操作")
			}
		case 112:
			var ids []string
			for _, j := range jobs {
				if j.Status == "running" {
					ids = append(ids, j.ID)
				}
			}
			if len(ids) > 0 {
				go func() {
					for _, id := range ids {
						if e := api("POST", "/app/cancel", map[string]string{"job": id}, nil); e != nil {
							post(func() { report(e.Error()) })
							return
						}
					}
					post(func() { report("已请求取消进行中的任务"); poll() })
				}()
			}
		case 110:
			refresh()
		}
		return 0
	case 0x10:
		closeApp()
		return 0
	case 0x2:
		cancel()
		call(user, "PostQuitMessage", 0)
		return 0
	}
	return call(user, "DefWindowProcW", h, uintptr(m), w, l)
}
func listProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x201:
		mouseX = int(int16(l))
		mouseY = int(int16(l >> 16))
		armed = true
	case 0x202:
		armed = false
	case 0x200:
		if armed && w&1 != 0 {
			x, y := int(int16(l)), int(int16(l>>16))
			if abs(x-mouseX)+abs(y-mouseY) > 8 {
				armed = false
				dragSelection()
				return 0
			}
		}
	case 0x100:
		if w == 13 {
			openSelected()
			return 0
		}
		if w == 8 {
			up()
			return 0
		}
		if w == 0x74 {
			refresh()
			return 0
		}
		if w == 0x2E && allLocal(chosen()) {
			call(user, "PostMessageW", window, 0x111, 106, 0)
			return 0
		}
	}
	return call(user, "CallWindowProcW", oldListProc, h, uintptr(m), w, l)
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func extract(dir, name string, data []byte) (string, error) {
	sum := sha256.Sum256(data)
	folder := filepath.Join(dir, fmt.Sprintf("runtime-%x", sum[:8]))
	if err := os.MkdirAll(folder, 0700); err != nil {
		return "", err
	}
	p := filepath.Join(folder, name)
	old, e := os.ReadFile(p)
	if e == nil && len(old) == len(data) && sha256.Sum256(old) == sum {
		return p, nil
	}
	if e == nil {
		return "", fmt.Errorf("内部组件校验失败：%s，请移除该文件后重试", p)
	}
	if !os.IsNotExist(e) {
		return "", e
	}
	tmp, e := os.CreateTemp(folder, "extract-")
	if e != nil {
		return "", e
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, e = tmp.Write(data); e != nil {
		tmp.Close()
		return "", e
	}
	if e = tmp.Close(); e != nil {
		return "", e
	}
	if e = os.Rename(tmpName, p); e != nil {
		return "", e
	}
	return p, nil
}

// Write a missing payload atomically. Never truncate a component another
// LocalBucket process could already have loaded.
func extractDLL(p string, data []byte) error {
	old, e := os.ReadFile(p)
	if e == nil {
		if len(old) == len(data) && sha256.Sum256(old) == sha256.Sum256(data) {
			return nil
		}
		return fmt.Errorf("内部运行库校验失败：%s", p)
	}
	if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), "runtime-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(data); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(name, p); e != nil {
		old, check := os.ReadFile(p)
		if check == nil && len(old) == len(data) && sha256.Sum256(old) == sha256.Sum256(data) {
			return nil
		}
		return e
	}
	return nil
}
func main() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if p := user.NewProc("SetProcessDpiAwarenessContext"); p.Find() == nil {
		if r, _, _ := p.Call(^uintptr(3)); r == 0 {
			call(user, "SetProcessDPIAware")
		}
	} else {
		call(user, "SetProcessDPIAware")
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "LocalBucket")
	binary, e := extract(root, "dufs.exe", dufs)
	if e == nil {
		e = extractDLL(filepath.Join(filepath.Dir(binary), "vcruntime140.dll"), vcRuntime)
	}

	for e == nil {
		service, e = node.Start(node.Options{DataDir: root, DufsPath: binary})
		if e == nil {
			break
		}
		existing := call(user, "FindWindowW", ptr(className), 0)
		if existing != 0 {
			call(user, "ShowWindow", existing, 9)
			call(user, "SetForegroundWindow", existing)
			return
		}
		if message("LocalBucket 无法启动", e.Error()+"\r\n请确认没有其他程序占用共享端口。点击重试可再次启动。", 0x15) != 4 {
			return
		}
		e = nil
	}
	if e != nil {
		message("LocalBucket 无法启动", e.Error(), 0x10)
		return
	}
	defer service.Close()
	hr := call(ole, "OleInitialize", 0)
	if int32(hr) < 0 {
		message("LocalBucket 无法启动", "系统拖放初始化失败", 0x10)
		return
	}
	defer call(ole, "OleUninitialize")
	initOLE()
	inst := call(kernel, "GetModuleHandleW", 0)
	icon := call(user, "LoadIconW", inst, 1)
	class, _ := syscall.UTF16PtrFromString(className)
	wc := wndclass{Size: uint32(unsafe.Sizeof(wndclass{})), Proc: syscall.NewCallback(wndProc), Instance: inst, Icon: icon, SmallIcon: icon, Cursor: call(user, "LoadCursorW", 0, 32512), Background: 6, Class: class}
	call(user, "RegisterClassExW", uintptr(unsafe.Pointer(&wc)))
	window = call(user, "CreateWindowExW", 0, uintptr(unsafe.Pointer(class)), ptr("LocalBucket"), 0x00cf0000, 100, 100, 1100, 720, 0, 0, inst, 0)
	if window == 0 {
		message("LocalBucket 无法启动", "无法创建窗口", 0x10)
		return
	}
	setUIFont(uint32(call(user, "GetDpiForWindow", window)))
	defer call(syscall.NewLazyDLL("gdi32.dll"), "DeleteObject", font)
	heading = control("STATIC", "共享空间", 0, 0)
	destination = control("STATIC", "", 0, 0)
	for i, label := range []string{"名称", "大小", "位置", "修改时间"} {
		columnLabels[i] = control("STATIC", label, 0, 0)
	}
	breadcrumb = control("STATIC", "所有文件", 0, 0)
	for _, b := range []struct {
		id    int
		label string
	}{{101, "←"}, {102, "共享空间"}, {103, "添加…"}, {105, "另存到…"}, {113, "更多"}, {111, "重试"}, {112, "取消任务"}} {
		control("BUTTON", b.label, 0x10000, b.id)
	}
	filter = control("EDIT", "", 0x00810080, 109)
	call(user, "SendMessageW", filter, 0x1501, 0, ptr("搜索当前列表"))
	list = control("LISTBOX", "共享空间文件列表", 0x00b109d1, 100)
	tabs := []int32{180, 230, 325}
	call(user, "SendMessageW", list, 0x192, 3, uintptr(unsafe.Pointer(&tabs[0])))
	call(user, "SendMessageW", list, 0x194, 1600, 0)
	status = control("STATIC", "文件拖出始终复制，原文件保留。", 0, 0)
	jobsBox = control("EDIT", "", 0x00200844, 0)
	emptyLabel = control("STATIC", "", 0, 0)
	oldListProc = call(user, "SetWindowLongPtrW", list, ^uintptr(3), syscall.NewCallback(listProc))
	target := newObject(3, targetVT, nil)
	if r := call(ole, "RegisterDragDrop", list, target); int32(r) < 0 {
		report("系统拖入注册失败；请使用“添加文件…”")
	}
	if r := call(ole, "RegisterDragDrop", window, target); int32(r) < 0 {
		report("窗口拖入注册失败；请将文件放到列表或使用添加按钮")
	}
	defer func() { call(ole, "RevokeDragDrop", list); call(ole, "RevokeDragDrop", window); release(target) }()
	layout()
	render()
	call(user, "SetTimer", window, 1, 2000, 0)
	call(user, "SetTimer", window, 2, 500, 0)
	call(user, "ShowWindow", window, 5)
	call(user, "UpdateWindow", window)
	refresh()
	onboarding()
	var m msg
	for {
		r := call(user, "GetMessageW", uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if (m.Hwnd == list && m.Message == 0x100 && (m.WParam == 13 || m.WParam == 8 || m.WParam == 0x74 || m.WParam == 0x2e)) || call(user, "IsDialogMessageW", window, uintptr(unsafe.Pointer(&m))) == 0 {
			call(user, "TranslateMessage", uintptr(unsafe.Pointer(&m)))
			call(user, "DispatchMessageW", uintptr(unsafe.Pointer(&m)))
			utf16Roots = nil
		}
	}
}
