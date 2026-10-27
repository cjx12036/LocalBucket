//go:build windows

package main

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

type guid struct {
	A    uint32
	B, C uint16
	D    [8]byte
}

var iidUnknown = guid{0, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
var iidData = guid{0x10e, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
var iidSource = guid{0x121, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
var iidEnum = guid{0x103, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
var iidTarget = guid{0x122, 0, 0, [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}

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
	Tymed, Pad      uint32
	Handle, Release uintptr
}

// COM objects live in the native heap; the registry owns any Go data until the
// final Release. No Go object address is retained by COM or native memory.
type comObject struct {
	VTable uintptr
	Kind   uint32
	Refs   int32
	Pos    uint32
}

var objectMu sync.Mutex
var objectPaths = map[uintptr][]string{}
var dataVT, sourceVT, enumVT, targetVT []uintptr

func hr(x uint32) uintptr { return uintptr(x) }
func newObject(kind uint32, vt []uintptr, paths []string) uintptr {
	h := call(kernel, "GetProcessHeap")
	p := call(kernel, "HeapAlloc", h, 8, unsafe.Sizeof(comObject{}))
	if p == 0 {
		return 0
	}
	o := (*comObject)(unsafe.Pointer(p))
	o.VTable = uintptr(unsafe.Pointer(&vt[0]))
	o.Kind = kind
	o.Refs = 1
	objectMu.Lock()
	objectPaths[p] = append([]string(nil), paths...)
	objectMu.Unlock()
	return p
}
func query(this, iid, out uintptr) uintptr {
	if out == 0 {
		return hr(0x80004003)
	}
	*(*uintptr)(unsafe.Pointer(out)) = 0
	if iid == 0 {
		return hr(0x80004003)
	}
	g := *(*guid)(unsafe.Pointer(iid))
	kind := (*comObject)(unsafe.Pointer(this)).Kind
	wanted := iidData
	switch kind {
	case 1:
		wanted = iidSource
	case 2:
		wanted = iidEnum
	case 3:
		wanted = iidTarget
	}
	if g == iidUnknown || g == wanted {
		*(*uintptr)(unsafe.Pointer(out)) = this
		addRef(this)
		return 0
	}
	return hr(0x80004002)
}
func addRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&(*comObject)(unsafe.Pointer(this)).Refs, 1))
}
func release(this uintptr) uintptr {
	if this == 0 {
		return 0
	}
	n := atomic.AddInt32(&(*comObject)(unsafe.Pointer(this)).Refs, -1)
	if n == 0 {
		objectMu.Lock()
		delete(objectPaths, this)
		objectMu.Unlock()
		call(kernel, "HeapFree", call(kernel, "GetProcessHeap"), 0, this)
	}
	return uintptr(n)
}
func queryData(this, f uintptr) uintptr {
	if f == 0 {
		return hr(0x80004003)
	}
	v := (*format)(unsafe.Pointer(f))
	if v.CF == 15 && v.Aspect == 1 && v.Tymed&1 != 0 && v.Index == -1 {
		return 0
	}
	return hr(0x80040064)
}
func dataGet(this, f, m uintptr) uintptr {
	if m == 0 {
		return hr(0x80004003)
	}
	*(*medium)(unsafe.Pointer(m)) = medium{}
	if r := queryData(this, f); r != 0 {
		return r
	}
	objectMu.Lock()
	paths := append([]string(nil), objectPaths[this]...)
	objectMu.Unlock()
	var names []uint16
	for _, p := range paths {
		n, e := syscall.UTF16FromString(p)
		if e != nil {
			return hr(0x80070057)
		}
		names = append(names, n...)
	}
	names = append(names, 0)
	size := 20 + len(names)*2
	h := call(kernel, "GlobalAlloc", 0x42, uintptr(size))
	if h == 0 {
		return hr(0x8007000e)
	}
	p := call(kernel, "GlobalLock", h)
	if p == 0 {
		call(kernel, "GlobalFree", h)
		return hr(0x80004005)
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(p)), size)
	binary.LittleEndian.PutUint32(buf[:4], 20)
	binary.LittleEndian.PutUint32(buf[16:20], 1)
	for i, n := range names {
		binary.LittleEndian.PutUint16(buf[20+i*2:], n)
	}
	call(kernel, "GlobalUnlock", h)
	*(*medium)(unsafe.Pointer(m)) = medium{Tymed: 1, Handle: h}
	return 0
}
func notImpl2(a, b uintptr) uintptr          { return hr(0x80004001) }
func notImpl3(a, b, c uintptr) uintptr       { return hr(0x80004001) }
func notImpl4(a, b, c, d uintptr) uintptr    { return hr(0x80004001) }
func noAdvise(a, b, c, d, e uintptr) uintptr { return hr(0x80040003) }
func canonical(a, b, c uintptr) uintptr {
	if c != 0 {
		(*format)(unsafe.Pointer(c)).Target = 0
	}
	return hr(0x80004001)
}
func enumFormats(this, direction, out uintptr) uintptr {
	if out == 0 {
		return hr(0x80004003)
	}
	*(*uintptr)(unsafe.Pointer(out)) = 0
	if direction != 1 {
		return hr(0x80004001)
	}
	p := newObject(2, enumVT, nil)
	if p == 0 {
		return hr(0x8007000e)
	}
	*(*uintptr)(unsafe.Pointer(out)) = p
	return 0
}
func enumNext(this, count, formats, fetched uintptr) uintptr {
	if fetched != 0 {
		*(*uint32)(unsafe.Pointer(fetched)) = 0
	}
	if formats == 0 || (count != 1 && fetched == 0) {
		return hr(0x80004003)
	}
	o := (*comObject)(unsafe.Pointer(this))
	n := uint32(0)
	if count > 0 && o.Pos == 0 {
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
	o := (*comObject)(unsafe.Pointer(this))
	if count == 0 {
		return 0
	}
	if count == 1 && o.Pos == 0 {
		o.Pos = 1
		return 0
	}
	o.Pos = 1
	return 1
}
func enumReset(this uintptr) uintptr { (*comObject)(unsafe.Pointer(this)).Pos = 0; return 0 }
func enumClone(this, out uintptr) uintptr {
	if out == 0 {
		return hr(0x80004003)
	}
	p := newObject(2, enumVT, nil)
	*(*uintptr)(unsafe.Pointer(out)) = p
	if p == 0 {
		return hr(0x8007000e)
	}
	(*comObject)(unsafe.Pointer(p)).Pos = (*comObject)(unsafe.Pointer(this)).Pos
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
func comCall(obj uintptr, slot int, args ...uintptr) uintptr {
	vt := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vt + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

var acceptingDrop bool

func targetEnter(this, data, keys, pt, effect uintptr) uintptr {
	f := format{CF: 15, Aspect: 1, Index: -1, Tymed: 1}
	acceptingDrop = effect != 0 && *(*uint32)(unsafe.Pointer(effect))&1 != 0 && selectedPeer != nil && comCall(data, 5, uintptr(unsafe.Pointer(&f))) == 0
	if effect != 0 {
		*(*uint32)(unsafe.Pointer(effect)) = 0
		if acceptingDrop {
			*(*uint32)(unsafe.Pointer(effect)) = 1
			report("添加到「" + selectedPeer.Name + " / " + currentPath + "」 — 松开鼠标复制文件")
			call(user, "SetWindowLongPtrW", list, ^uintptr(19), call(user, "GetWindowLongPtrW", list, ^uintptr(19))|0x200)
			call(user, "SetWindowPos", list, 0, 0, 0, 0, 0, 0x27)
		} else {
			report("请先打开要存放文件的电脑")
		}
	}
	return 0
}
func targetOver(this, keys, pt, effect uintptr) uintptr {
	if effect != 0 {
		allowed := *(*uint32)(unsafe.Pointer(effect))&1 != 0
		*(*uint32)(unsafe.Pointer(effect)) = 0
		if acceptingDrop && allowed {
			*(*uint32)(unsafe.Pointer(effect)) = 1
		}
	}
	return 0
}
func targetLeave(this uintptr) uintptr { clearDropHighlight(); acceptingDrop = false; return 0 }
func clearDropHighlight() {
	call(user, "SetWindowLongPtrW", list, ^uintptr(19), call(user, "GetWindowLongPtrW", list, ^uintptr(19))&^0x200)
	call(user, "SetWindowPos", list, 0, 0, 0, 0, 0, 0x27)
}
func targetDrop(this, data, keys, pt, effect uintptr) uintptr {
	defer clearDropHighlight()
	if effect != 0 {
		*(*uint32)(unsafe.Pointer(effect)) = 0
	}
	if !acceptingDrop {
		return 0
	}
	acceptingDrop = false
	if dragActive && selectedPeer != nil && dragPeer == selectedPeer.ID && dragPath == currentPath {
		report("文件已在这里，无需重复添加")
		return 0
	}
	f := format{CF: 15, Aspect: 1, Index: -1, Tymed: 1}
	var m medium
	r := comCall(data, 3, uintptr(unsafe.Pointer(&f)), uintptr(unsafe.Pointer(&m)))
	if int32(r) < 0 {
		report("无法读取拖入文件，请使用“添加文件…”")
		return 0
	}
	defer call(ole, "ReleaseStgMedium", uintptr(unsafe.Pointer(&m)))
	if m.Tymed != 1 || m.Handle == 0 {
		return 0
	}
	n := call(shell, "DragQueryFileW", m.Handle, 0xffffffff, 0, 0)
	var sources []string
	for i := uintptr(0); i < n; i++ {
		length := call(shell, "DragQueryFileW", m.Handle, i, 0, 0)
		if length > 32767 {
			continue
		}
		buf := make([]uint16, length+1)
		call(shell, "DragQueryFileW", m.Handle, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		sources = append(sources, syscall.UTF16ToString(buf))
	}
	importSources(sources)
	if effect != 0 {
		*(*uint32)(unsafe.Pointer(effect)) = 1
	}
	return 0
}
func initOLE() {
	cb := syscall.NewCallback
	dataVT = []uintptr{cb(query), cb(addRef), cb(release), cb(dataGet), cb(notImpl3), cb(queryData), cb(canonical), cb(notImpl4), cb(enumFormats), cb(noAdvise), cb(notImpl2), cb(notImpl2)}
	sourceVT = []uintptr{cb(query), cb(addRef), cb(release), cb(continueDrag), cb(feedback)}
	enumVT = []uintptr{cb(query), cb(addRef), cb(release), cb(enumNext), cb(enumSkip), cb(enumReset), cb(enumClone)}
	targetVT = []uintptr{cb(query), cb(addRef), cb(release), cb(targetEnter), cb(targetOver), cb(targetLeave), cb(targetDrop)}
}
func runDrag(paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	o := newObject(0, dataVT, paths)
	s := newObject(1, sourceVT, nil)
	if o == 0 || s == 0 {
		release(o)
		release(s)
		report("内存不足，无法拖出文件")
		return false
	}
	defer release(o)
	defer release(s)
	dragActive = true
	dragPeer = selectedPeer.ID
	dragPath = currentPath
	defer func() { dragActive = false }()
	var effect uint32
	r := call(ole, "DoDragDrop", o, s, 1, uintptr(unsafe.Pointer(&effect)))
	if int32(r) < 0 {
		report("系统拖出失败，请使用“另存到…”")
	} else if effect == 1 {
		report("已复制文件，原文件保留")
		return true
	}
	return false
}
