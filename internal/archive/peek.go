package archive

import (
	"bufio"
	"io"
)

// newPeekReader wraps r in a buffered reader so callers can sniff magic bytes
// without consuming them. If r is already buffered it is reused.
func newPeekReader(r io.Reader) *bufio.Reader {
	if br, ok := r.(*bufio.Reader); ok {
		return br
	}
	return bufio.NewReaderSize(r, 64<<10)
}
