//go:build !windows

package statedir_test

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestModeNote: the note of a mode tells the permissions and the setuid, setgid and sticky bits apart.
func TestModeNote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantNote string
		mode     fs.FileMode
	}{
		{name: "private file", mode: 0o600, wantNote: "[-rw-------]"},
		{name: "readable file", mode: 0o644, wantNote: "[-rw-r--r--]"},
		{name: "setuid", mode: 0o755 | fs.ModeSetuid, wantNote: "[urwxr-xr-x]"},
		{name: "setgid", mode: 0o755 | fs.ModeSetgid, wantNote: "[grwxr-xr-x]"},
		{name: "sticky", mode: 0o755 | fs.ModeSticky, wantNote: "[trwxr-xr-x]"},
		{name: "the type bits are not part of the note", mode: 0o700 | fs.ModeDir, wantNote: "[-rwx------]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantNote, modeNote(tt.mode))
		})
	}
}
