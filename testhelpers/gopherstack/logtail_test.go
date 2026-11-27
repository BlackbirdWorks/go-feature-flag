package gopherstack_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

var errRead = errors.New("connection lost")

func TestTailLog(t *testing.T) {
	t.Parallel()

	const maxLine = 10

	tests := []struct {
		reader            io.Reader
		name              string
		wantHeader        string
		wantNotInText     string
		wantLines         []string
		wantCutLines      int
		wantWant          int
		wantPartial       bool
		wantReadErrIsRead bool
	}{
		{
			name:       "fewer lines than asked",
			reader:     strings.NewReader("a\nb\n"),
			wantWant:   3,
			wantLines:  []string{"a", "b"},
			wantHeader: "last 2 lines of the log of the emulator:",
		},
		{
			name:       "more lines than asked keeps the last ones",
			reader:     strings.NewReader("a\nb\nc\nd\ne"),
			wantWant:   3,
			wantLines:  []string{"c", "d", "e"},
			wantHeader: "last 3 lines of the log of the emulator:",
		},
		{
			name:          "a line over the cap is cut and says so",
			reader:        strings.NewReader("a\n" + strings.Repeat("x", 25) + "\nz\n"),
			wantWant:      5,
			wantLines:     []string{"a", strings.Repeat("x", maxLine) + " [line cut at 10 bytes]", "z"},
			wantCutLines:  1,
			wantPartial:   true,
			wantHeader:    "PARTIAL: 1 lines longer than 10 bytes were cut",
			wantNotInText: strings.Repeat("x", maxLine+1),
		},
		{
			name:       "a line of exactly the cap is not cut",
			reader:     strings.NewReader(strings.Repeat("y", maxLine) + "\nz\n"),
			wantWant:   5,
			wantLines:  []string{strings.Repeat("y", maxLine), "z"},
			wantHeader: "last 2 lines of the log of the emulator:",
		},
		{
			name:              "a read that fails is partial and is not called the end of the log",
			reader:            io.MultiReader(strings.NewReader("a\nb\npartial"), iotest.ErrReader(errRead)),
			wantWant:          5,
			wantLines:         []string{"a", "b", "partial"},
			wantPartial:       true,
			wantReadErrIsRead: true,
			wantHeader: "PARTIAL log of the emulator: the read failed (connection lost); " +
				"these are the last 3 lines read, not the end of the log",
			wantNotInText: "last 3 lines of the log of the emulator",
		},
		{
			name:       "an empty log",
			reader:     strings.NewReader(""),
			wantWant:   5,
			wantLines:  []string{},
			wantHeader: "last 0 lines of the log of the emulator:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tail := gopherstack.TailLog(tt.reader, tt.wantWant, maxLine)

			assert.Equal(t, tt.wantLines, tail.Lines)
			assert.Equal(t, tt.wantCutLines, tail.CutLines)
			assert.Equal(t, tt.wantPartial, tail.Partial())
			assert.Contains(t, tail.String(), tt.wantHeader)

			if tt.wantNotInText != "" {
				assert.NotContains(t, tail.String(), tt.wantNotInText)
			}

			if tt.wantReadErrIsRead {
				require.ErrorIs(t, tail.ReadErr, errRead)
			} else {
				require.NoError(t, tail.ReadErr)
			}
		})
	}
}
