package node

import (
	"errors"
	"syscall"
	"unsafe"
)

type shellFileOp struct {
	Window    uintptr
	Operation uint32
	From, To  *uint16
	Flags     uint16
	Aborted   int32
	Mappings  uintptr
	Title     *uint16
}

func trash(p string) error {
	from, e := syscall.UTF16FromString(p)
	if e != nil {
		return e
	}
	from = append(from, 0)
	op := shellFileOp{Operation: 3, From: &from[0], Flags: 0x40 | 0x10 | 0x4 | 0x400}
	r, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW").Call(uintptr(unsafe.Pointer(&op)))
	if r != 0 || op.Aborted != 0 {
		return errors.New("无法移入回收站")
	}
	return nil
}
