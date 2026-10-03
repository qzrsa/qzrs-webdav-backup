//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package api

import "syscall"

// diskUsage reports total and free bytes on the filesystem holding path.
func diskUsage(path string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	total = st.Blocks * bsize
	// Bavail excludes blocks reserved for root, which is what a backup job can
	// actually use.
	free = st.Bavail * bsize
	return total, free, nil
}
