//go:build windows

package pvf

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateFileW  = kernel32.NewProc("CreateFileW")
	procCreateFileM  = kernel32.NewProc("CreateFileMappingW")
	procMapView      = kernel32.NewProc("MapViewOfFile")
	procUnmapView    = kernel32.NewProc("UnmapViewOfFile")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
)

const (
	genericRead         = 0x80000000
	fileShareRead       = 1
	openExisting        = 3
	pageReadonly        = 0x02
	fileMapRead         = 0x0004
	invalidHandleValue  = ^uintptr(0)
)

// mmapFile maps the whole file read-only. The returned slice aliases the
// mapping; it must never be written. Tools are short-lived, so the mapping is
// intentionally never unmapped: the OS reclaims it at process exit. Loading a
// 390 MB archive via ReadFile doubles peak memory with the heap copy; the
// mapping has no copy at all.
func mmapFile(path string) ([]byte, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	hFile, _, err := procCreateFileW.Call(
		uintptr(unsafe.Pointer(p)), genericRead, fileShareRead, 0, openExisting, 0, 0)
	if hFile == invalidHandleValue || hFile == 0 {
		return nil, fmt.Errorf("mmap open: %v", err)
	}
	defer procCloseHandle.Call(hFile)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	size := uint64(info.Size())
	hMap, _, err := procCreateFileM.Call(hFile, 0, pageReadonly, uintptr(size>>32), uintptr(size&0xffffffff), 0)
	if hMap == 0 {
		return nil, fmt.Errorf("mmap mapping: %v", err)
	}
	defer procCloseHandle.Call(hMap)
	view, _, err := procMapView.Call(hMap, fileMapRead, 0, 0, 0)
	if view == 0 {
		return nil, fmt.Errorf("mmap view: %v", err)
	}
	// Keep the view alive for the process lifetime; the slice header aliases
	// the mapped pages without copying them.
	return unsafe.Slice((*byte)(unsafe.Pointer(view)), int(size)), nil
}

// loadFileBytes prefers a read-only mapping and falls back to a heap copy.
func loadFileBytes(path string, limit int64) ([]byte, error) {
	if data, err := mmapFile(path); err == nil {
		return data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: got %d want <= %d", ErrTooLarge, len(data), limit)
	}
	return data, nil
}
