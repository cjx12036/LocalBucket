//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

type openFileName struct {
	Size                   uint32
	Owner, Instance        uintptr
	Filter, CustomFilter   *uint16
	MaxCustom, FilterIndex uint32
	File                   *uint16
	MaxFile                uint32
	FileTitle              *uint16
	MaxFileTitle           uint32
	InitialDir, Title      *uint16
	Flags                  uint32
	FileOffset, Extension  uint16
	DefaultExt             *uint16
	CustomData             uintptr
	Hook                   uintptr
	Template               *uint16
	Reserved               uintptr
	Reserved2, FlagsEx     uint32
}

func pickFiles() []string {
	buf := make([]uint16, 65536)
	filter, _ := syscall.UTF16FromString("所有文件")
	filter = append(filter, []uint16{'*', 0, 0}...)
	title, _ := syscall.UTF16PtrFromString("选择要添加的文件")
	o := openFileName{Size: uint32(unsafe.Sizeof(openFileName{})), Owner: window, Filter: &filter[0], File: &buf[0], MaxFile: uint32(len(buf)), Title: title, Flags: 0x80000 | 0x1000 | 0x800 | 0x200 | 0x8}
	if call(comdlg, "GetOpenFileNameW", uintptr(unsafe.Pointer(&o))) == 0 {
		if code := call(comdlg, "CommDlgExtendedError"); code != 0 {
			report("无法打开文件选择器，请尝试拖入文件")
		}
		return nil
	}
	var parts []string
	start := 0
	for i, v := range buf {
		if v == 0 {
			if i == start {
				break
			}
			parts = append(parts, syscall.UTF16ToString(buf[start:i]))
			start = i + 1
		}
	}
	if len(parts) <= 1 {
		return parts
	}
	var paths []string
	for _, name := range parts[1:] {
		paths = append(paths, strings.TrimRight(parts[0], "\\")+"\\"+name)
	}
	return paths
}

type browseInfo struct {
	Owner, Root, Display, Title uintptr
	Flags                       uint32
	Callback, Param             uintptr
	Image                       int32
}

func pickFolder(title string) string {
	buf := make([]uint16, 32768)
	b := browseInfo{Owner: window, Display: uintptr(unsafe.Pointer(&buf[0])), Title: ptr(title), Flags: 0x1 | 0x40 | 0x10}
	pidl := call(shell, "SHBrowseForFolderW", uintptr(unsafe.Pointer(&b)))
	if pidl == 0 {
		return ""
	}
	defer call(ole, "CoTaskMemFree", pidl)
	if call(shell, "SHGetPathFromIDListW", pidl, uintptr(unsafe.Pointer(&buf[0]))) == 0 {
		report("请选择本机文件夹")
		return ""
	}
	return syscall.UTF16ToString(buf)
}
