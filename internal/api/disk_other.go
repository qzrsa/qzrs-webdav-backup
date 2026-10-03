//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package api

import "errors"

// diskUsage is unavailable on this platform. The console hides the disk gauge
// when Supported is false, so this is a graceful degradation rather than a
// broken feature.
func diskUsage(path string) (total, free uint64, err error) {
	return 0, 0, errors.New("disk usage is not supported on this platform")
}
