//go:build unix

package freespace

import (
	"fmt"
	"syscall"
)

// Available reports the free bytes for unprivileged users on the filesystem
// at path (statfs Bavail, not Bfree: root-reserved blocks don't help us).
func Available(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
