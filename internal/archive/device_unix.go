//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package archive

import (
	"io/fs"
	"syscall"
)

// statDevice extracts the device id from a FileInfo, used to avoid crossing
// mount points when OneFileSystem is enabled.
func statDevice(fi fs.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
