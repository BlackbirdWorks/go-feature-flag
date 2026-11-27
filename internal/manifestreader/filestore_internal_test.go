package manifestreader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errDisk    = errors.New("disk on fire")
	errEscaped = errors.New(escapeMessage)
)

// scriptedReader serves data in pieces of at most piece bytes and counts what it served. after runs once the
// first piece was served; failAt makes the read that would pass that offset fail with errDisk.
type scriptedReader struct {
	after  func()
	data   []byte
	served int
	piece  int
	failAt int
}

func (r *scriptedReader) Read(buf []byte) (int, error) {
	if r.failAt > 0 && r.served >= r.failAt {
		return 0, errDisk
	}

	if r.served >= len(r.data) {
		return 0, io.EOF
	}

	count := min(len(buf), r.piece, len(r.data)-r.served)
	copy(buf, r.data[r.served:r.served+count])
	r.served += count

	if r.after != nil {
		r.after()
		r.after = nil
	}

	return count, nil
}

func TestReadBounded(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("abcdefghij"), 10)

	tests := []struct {
		wantErr    error
		name       string
		limit      int64
		size       int64
		failAt     int
		wantServed int
		wantBytes  bool
	}{
		{name: "exactly at the limit", limit: 100, size: 100, wantBytes: true, wantServed: 100},
		{name: "no size hint", limit: 100, size: 0, wantBytes: true, wantServed: 100},
		{
			name:       "one over: one byte past the limit is read, no more",
			limit:      99,
			size:       99,
			wantErr:    ErrTooLarge,
			wantServed: 100,
		},
		{name: "far over: reads stop at limit plus one", limit: 10, size: 10, wantErr: ErrTooLarge, wantServed: 11},
		{name: "limit zero", limit: 0, size: 0, wantErr: ErrTooLarge, wantServed: 1},
		{name: "no limit", limit: math.MaxInt64, size: 100, wantBytes: true, wantServed: 100},
		{name: "a read error", limit: 100, size: 100, failAt: 28, wantErr: errDisk, wantServed: 28},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &scriptedReader{data: data, piece: 7, failAt: tt.failAt}
			got, err := readBounded(t.Context(), src, tt.size, tt.limit)

			assert.Equal(t, tt.wantServed, src.served, "bytes served by the file")

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, data, got)
		})
	}
}

func TestReadBounded_ContextEndsBetweenReads(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	src := &scriptedReader{data: bytes.Repeat([]byte("x"), 1000), piece: 10, after: cancel}

	got, err := readBounded(ctx, src, 1000, 1000)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, got)
	assert.Equal(t, 10, src.served, "no read after the context ended")
}

func TestHashFile(t *testing.T) {
	t.Parallel()

	data := bytes.Repeat([]byte("abcdefghij"), 10)

	tests := []struct {
		wantErr    error
		name       string
		limit      int64
		failAt     int
		wantServed int
		wantSum    bool
	}{
		{name: "at the limit", limit: 100, wantSum: true, wantServed: 100},
		{name: "no limit", limit: math.MaxInt64, wantSum: true, wantServed: 100},
		{name: "over the limit stops one byte past it", limit: 40, wantErr: ErrTooLarge, wantServed: 41},
		{name: "a read error", limit: 100, failAt: 26, wantErr: errDisk, wantServed: 26},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			src := &scriptedReader{data: data, piece: 13, failAt: tt.failAt}
			sum, err := hashFile(t.Context(), src, tt.limit)

			assert.Equal(t, tt.wantServed, src.served)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, sha256.Sum256(data), sum)
		})
	}
}

func TestHashFile_ContextEndsBetweenReads(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	src := &scriptedReader{data: bytes.Repeat([]byte("x"), 1000), piece: 10, after: cancel}

	_, err := hashFile(ctx, src, 1000)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 10, src.served)
}

func TestClassifyFileError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err           error
		wantIs        error
		name          string
		wantReason    string
		wantRetryable bool
	}{
		{name: "does not exist", err: &fsError{syscall.ENOENT}, wantIs: ErrNotFound},
		{name: "a parent is a file", err: &fsError{syscall.ENOTDIR}, wantIs: ErrNotFound},
		{name: "permission", err: &fsError{syscall.EACCES}, wantReason: ReasonDenied},
		{name: "operation not permitted", err: &fsError{syscall.EPERM}, wantReason: ReasonDenied},
		{name: "symlink loop", err: &fsError{syscall.ELOOP}, wantReason: ReasonDenied},
		{
			name: "a socket or a device without driver", err: &fsError{syscall.ENXIO}, wantReason: ReasonDenied,
			wantIs: ErrNotRegular,
		},
		{name: "no such device", err: &fsError{syscall.ENODEV}, wantReason: ReasonDenied, wantIs: ErrNotRegular},
		{name: "name too long", err: &fsError{syscall.ENAMETOOLONG}, wantReason: ReasonDenied, wantIs: ErrKeyRefused},
		{name: "not a regular file", err: ErrNotRegular, wantReason: ReasonDenied},
		{name: "I/O error", err: &fsError{syscall.EIO}, wantReason: ReasonError, wantRetryable: true},
		{name: "too many open files", err: &fsError{syscall.EMFILE}, wantReason: ReasonError, wantRetryable: true},
		{
			name:          "too many open files, system",
			err:           &fsError{syscall.ENFILE},
			wantReason:    ReasonError,
			wantRetryable: true,
		},
		{name: "stale handle", err: &fsError{syscall.ESTALE}, wantReason: ReasonError, wantRetryable: true},
		{name: "out of memory", err: &fsError{syscall.ENOMEM}, wantReason: ReasonError, wantRetryable: true},
		{name: "anything else", err: errDisk, wantReason: ReasonError, wantRetryable: true},
		{
			name:       "an I/O error on a file whose name is the escape message",
			err:        &fs.PathError{Op: "open", Path: "dir/" + escapeMessage + ".json", Err: syscall.EIO},
			wantReason: ReasonError, wantRetryable: true,
		},
		{
			name:       "the escape error of os.Root, as a path error",
			err:        &fs.PathError{Op: "openat", Path: "link", Err: errEscaped},
			wantReason: ReasonDenied, wantIs: ErrOutsideRoot,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := classifyFileError(tt.err)

			if tt.wantReason == "" {
				assert.Equal(t, tt.wantIs, got)

				return
			}

			fetchErr, ok := errors.AsType[*FetchError](got)
			require.True(t, ok, "%T", got)
			assert.Equal(t, tt.wantReason, fetchErr.Reason)
			assert.Equal(t, tt.wantRetryable, fetchErr.Retryable)
			assert.False(t, fetchErr.Throttled)

			if tt.wantIs != nil {
				assert.ErrorIs(t, got, tt.wantIs)
			}
		})
	}
}

// TestClassifyFileError_Root: the root directory has its own table. A root that is missing, or that answers
// with an I/O error, may be there at the next poll (a volume mounted late): a retryable error, never "not found"
// (the object is not what is missing) and never denied. Only a permission error is final.
func TestClassifyFileError_Root(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantReason    string
		errno         syscall.Errno
		wantRetryable bool
	}{
		{errno: syscall.ENOENT, wantReason: ReasonError, wantRetryable: true},
		{errno: syscall.ENOTDIR, wantReason: ReasonError, wantRetryable: true},
		{errno: syscall.EMFILE, wantReason: ReasonError, wantRetryable: true},
		{errno: syscall.ENFILE, wantReason: ReasonError, wantRetryable: true},
		{errno: syscall.EIO, wantReason: ReasonError, wantRetryable: true},
		{errno: syscall.ESTALE, wantReason: ReasonError, wantRetryable: true},
		{errno: syscall.ENOMEM, wantReason: ReasonError, wantRetryable: true},
		{errno: syscall.EACCES, wantReason: ReasonDenied},
		{errno: syscall.EPERM, wantReason: ReasonDenied},
	}

	for _, tt := range tests {
		t.Run(tt.errno.Error(), func(t *testing.T) {
			t.Parallel()

			cause := fmt.Errorf("%w: %w", ErrRootUnavailable, &fsError{tt.errno})
			got := classifyFileError(cause)

			fetchErr, ok := errors.AsType[*FetchError](got)
			require.True(t, ok, "%T: %v", got, got)
			assert.Equal(t, tt.wantReason, fetchErr.Reason)
			assert.Equal(t, tt.wantRetryable, fetchErr.Retryable)
			assert.False(t, fetchErr.Throttled)
			require.NotErrorIs(t, got, ErrNotFound)
			require.ErrorIs(t, got, ErrRootUnavailable, "the root stays a cause")
			require.ErrorIs(t, got, tt.errno)
		})
	}
}

// fsError is an errno as the file system operations return it.
type fsError struct{ errno syscall.Errno }

func (e *fsError) Error() string { return e.errno.Error() }
func (e *fsError) Unwrap() error { return e.errno }

func TestFileFailure(t *testing.T) {
	t.Parallel()

	ended, cancel := context.WithCancel(t.Context())
	cancel()

	live := t.Context()

	tests := []struct {
		ctx           context.Context //nolint:containedctx // the table is the list of contexts
		err           error
		wantIs        error
		name          string
		wantRetryable bool
		wantFetch     bool
	}{
		{name: "too large passes through", ctx: live, err: ErrTooLarge, wantIs: ErrTooLarge},
		{
			name:          "the context ended",
			ctx:           ended,
			err:           context.Canceled,
			wantFetch:     true,
			wantRetryable: true,
			wantIs:        context.Canceled,
		},
		{
			name:      "an error after the context ended is classified as it is",
			ctx:       ended,
			err:       &fsError{syscall.EACCES},
			wantFetch: true,
		},
		{name: "missing", ctx: live, err: &fsError{syscall.ENOENT}, wantIs: ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := fileFailure(tt.ctx, tt.err)
			if tt.wantIs != nil {
				require.ErrorIs(t, got, tt.wantIs)
			}

			fetchErr, ok := errors.AsType[*FetchError](got)
			assert.Equal(t, tt.wantFetch, ok)

			if ok {
				assert.Equal(t, tt.wantRetryable, fetchErr.Retryable)
			}
		})
	}
}

func TestValidFileKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{key: "a", want: true},
		{key: "a/b/c.json", want: true},
		{key: "..data/manifest.yaml", want: true},
		{key: "a/..b/c", want: true},
		{key: "with space/é", want: true},
		{key: ""},
		{key: "."},
		{key: ".."},
		{key: "/a"},
		{key: "a/"},
		{key: "a//b"},
		{key: "a/./b"},
		{key: "a/../b"},
		{key: "../a"},
		{key: "a\\b"},
		{key: "a\x00b"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.key), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, ValidFileKey(tt.key))
		})
	}
}
