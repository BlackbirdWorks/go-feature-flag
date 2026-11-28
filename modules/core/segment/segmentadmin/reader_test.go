package segmentadmin_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

type wantRecord struct {
	id     string
	reason segmentadmin.Reason
	line   int
}

func TestNewReader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		opts    segmentadmin.ReaderOptions
	}{
		{name: "defaults"},
		{name: "bad format", opts: segmentadmin.ReaderOptions{Format: "xml"}, wantErr: segmentadmin.ErrBadOptions},
		{name: "bad column", opts: segmentadmin.ReaderOptions{Column: -1}, wantErr: segmentadmin.ErrBadOptions},
		{
			name:    "bad line bound",
			opts:    segmentadmin.ReaderOptions{MaxLineBytes: -1},
			wantErr: segmentadmin.ErrBadOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := segmentadmin.NewReader(strings.NewReader(""), tt.opts)
			if tt.wantErr == nil {
				assert.NoError(t, err)

				return
			}

			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestReader_Next(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []wantRecord
		opts  segmentadmin.ReaderOptions
	}{
		{
			name: "lines", input: "a\r\n\n  \nb",
			want: []wantRecord{
				{id: "a", line: 1}, {reason: segmentadmin.ReasonEmpty, line: 2},
				{reason: segmentadmin.ReasonEmpty, line: 3}, {id: "b", line: 4},
			},
		},
		{
			name: "too long line is skipped to its end", input: strings.Repeat("x", 100) + "\nok\n",
			opts: segmentadmin.ReaderOptions{MaxLineBytes: 20},
			want: []wantRecord{{reason: segmentadmin.ReasonTooLong, line: 1}, {id: "ok", line: 2}},
		},
		{
			name: "too long last line", input: "ok\n" + strings.Repeat("x", 100),
			opts: segmentadmin.ReaderOptions{MaxLineBytes: 20},
			want: []wantRecord{{id: "ok", line: 1}, {reason: segmentadmin.ReasonTooLong, line: 2}},
		},
		{
			name: "csv column", input: "1,\"a,b\",x\n2\n3,,x\n\n",
			opts: segmentadmin.ReaderOptions{Format: segmentadmin.FormatCSV, Column: 2},
			want: []wantRecord{
				{id: "a,b", line: 1}, {reason: segmentadmin.ReasonColumn, line: 2},
				{reason: segmentadmin.ReasonEmpty, line: 3}, {reason: segmentadmin.ReasonEmpty, line: 4},
			},
		},
		{
			name: "csv default column and lazy quotes", input: "a,b\n\"x\"y,z\n",
			opts: segmentadmin.ReaderOptions{Format: segmentadmin.FormatCSV},
			want: []wantRecord{{id: "a", line: 1}, {id: "x\"y,z", line: 2}},
		},
		{name: "empty input"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader, err := segmentadmin.NewReader(strings.NewReader(tt.input), tt.opts)
			require.NoError(t, err)

			var got []wantRecord

			for {
				rec, nextErr := reader.Next()
				if errors.Is(nextErr, io.EOF) {
					break
				}

				require.NoError(t, nextErr)

				got = append(got, wantRecord{id: rec.ID, reason: rec.Reason, line: rec.Line})
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

type failingReader struct{}

var errBroken = errors.New("broken input")

func (failingReader) Read([]byte) (int, error) { return 0, errBroken }

func TestReader_Next_sourceError(t *testing.T) {
	t.Parallel()

	reader, err := segmentadmin.NewReader(failingReader{}, segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	_, err = reader.Next()
	assert.ErrorIs(t, err, errBroken)
}
