//go:build unix

package manifestreader

import (
	"errors"
	"os"
	"syscall"
)

// openFlags opens read-only and never blocks: opening a FIFO for reading would otherwise wait for a writer
// forever. A regular file is not affected. A socket cannot be opened at all (ENXIO).
const openFlags = os.O_RDONLY | syscall.O_NONBLOCK

// isIrregularOpen reports an open that failed because the file is a socket (or a device without a driver).
func isIrregularOpen(err error) bool {
	return errors.Is(err, syscall.ENXIO)
}
