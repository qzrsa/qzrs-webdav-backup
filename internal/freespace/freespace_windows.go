//go:build windows

package freespace

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
)

// Available reports the free bytes for the calling user on the volume holding
// path, via GetDiskFreeSpaceExW.
func Available(path string) (uint64, error) {
	p16, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, fmt.Errorf("invalid path %q: %w", path, err)
	}
	var free, total, totalFree uint64
	r1, _, e1 := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p16)),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r1 == 0 {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx %s: %v", path, e1)
	}
	return free, nil
}
