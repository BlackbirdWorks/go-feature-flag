//go:build !unix

package manifestreader

import (
	"errors"
	"os"
	"syscall"
)

// openFlags opens read-only. There are no FIFOs to wait on; Windows device names are refused by os.Root.
const openFlags = os.O_RDONLY

// isIrregularOpen reports an open that failed because the file is not an ordinary one.
func isIrregularOpen(err error) bool {
	return errors.Is(err, syscall.ENXIO)
}
