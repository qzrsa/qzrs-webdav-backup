//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly)

package archive

import "io/fs"

// statDevice is a no-op on platforms without st_dev. OneFileSystem is then
// silently ignored rather than failing the whole backup.
func statDevice(fi fs.FileInfo) (uint64, bool) { return 0, false }
