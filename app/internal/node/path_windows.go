//go:build windows

package node

import (
	"os"
	"syscall"
)

func isReparse(p string, _ os.FileInfo) bool {
	v, e := syscall.UTF16PtrFromString(p)
	if e != nil {
		return true
	}
	a, e := syscall.GetFileAttributes(v)
	return e != nil || a&0x400 != 0
}
