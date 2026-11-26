//go:build !windows

package statedir

import (
	"fmt"
	"io/fs"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type modeInfo struct{ mode fs.FileMode }

func (modeInfo) Name() string        { return "dir" }
func (modeInfo) Size() int64         { return 0 }
func (i modeInfo) Mode() fs.FileMode { return i.mode }
func (modeInfo) ModTime() time.Time  { return time.Time{} }
func (i modeInfo) IsDir() bool       { return i.mode.IsDir() }
func (modeInfo) Sys() any            { return nil }

func TestWorldWritable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode fs.FileMode
		want bool
	}{
		{name: "private", mode: fs.ModeDir | 0o700},
		{name: "group-writable", mode: fs.ModeDir | 0o770},
		{name: "readable by everybody", mode: fs.ModeDir | 0o755},
		{name: "writable by others", mode: fs.ModeDir | 0o702, want: true},
		{name: "writable by everybody", mode: fs.ModeDir | 0o777, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, worldWritable(modeInfo{mode: tt.mode}))
		})
	}
}

func TestIsNoSpaceAndReadOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err          error
		name         string
		wantNoSpace  bool
		wantReadOnly bool
	}{
		{name: "a full disk", err: syscall.ENOSPC, wantNoSpace: true},
		{name: "a read-only file system", err: syscall.EROFS, wantReadOnly: true},
		{name: "an I/O error", err: syscall.EIO},
		{name: "a wrapped full disk", err: fmt.Errorf("write: %w", syscall.ENOSPC), wantNoSpace: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantNoSpace, isNoSpace(tt.err))
			assert.Equal(t, tt.wantReadOnly, isReadOnly(tt.err))
		})
	}
}
