//go:build unix

package manifestreader

import (
	"errors"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassifyFileError_SocketOpenErrors: opening a socket fails with ENXIO on Linux and with EOPNOTSUPP on macOS
// and the BSDs; both are a file that is not regular, and neither is retried as a transient error.
func TestClassifyFileError_SocketOpenErrors(t *testing.T) {
	t.Parallel()

	for _, errno := range []syscall.Errno{syscall.ENXIO, syscall.EOPNOTSUPP} {
		t.Run(errno.Error(), func(t *testing.T) {
			t.Parallel()

			got := classifyFileError(&fsError{errno})

			fetchErr, ok := errors.AsType[*FetchError](got)
			require.True(t, ok, "%T", got)
			assert.Equal(t, ReasonDenied, fetchErr.Reason)
			assert.False(t, fetchErr.Retryable)
			require.ErrorIs(t, got, ErrNotRegular)
			assert.ErrorIs(t, got, errno)
		})
	}
}
