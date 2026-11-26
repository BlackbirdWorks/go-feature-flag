//go:build !windows

package statedir

import (
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

	assert.True(t, isNoSpace(syscall.ENOSPC))
	assert.False(t, isNoSpace(syscall.EIO))
	assert.True(t, isReadOnly(syscall.EROFS))
	assert.False(t, isReadOnly(syscall.EIO))
}
