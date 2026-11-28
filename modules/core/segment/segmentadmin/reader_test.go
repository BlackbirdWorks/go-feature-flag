package segmentadmin_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

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
		{
			name: "a byte order mark at the start is not part of the first id", input: "\xef\xbb\xbfa\nb\n",
			want: []wantRecord{{id: "a", line: 1}, {id: "b", line: 2}},
		},
		{
			name: "a byte order mark at the start of a csv", input: "\xef\xbb\xbf1,a\n",
			opts: segmentadmin.ReaderOptions{Format: segmentadmin.FormatCSV, Column: 2},
			want: []wantRecord{{id: "a", line: 1}},
		},
		{
			name: "a byte order mark on the second line stays", input: "a\n\xef\xbb\xbfb\n",
			want: []wantRecord{{id: "a", line: 1}, {id: "\xef\xbb\xbfb", line: 2}},
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

func TestNewSliceSource(t *testing.T) {
	t.Parallel()

	source := segmentadmin.NewSliceSource([]string{lineBreakID, "c"})

	first, err := source.Next()
	require.NoError(t, err)
	assert.Equal(t, segmentadmin.Record{ID: lineBreakID, Line: 1}, first, "an id is never split or joined")

	second, err := source.Next()
	require.NoError(t, err)
	assert.Equal(t, segmentadmin.Record{ID: "c", Line: 2}, second)

	_, err = source.Next()
	require.ErrorIs(t, err, io.EOF)
}

func TestWithContext(t *testing.T) {
	t.Parallel()

	t.Run("data goes through", func(t *testing.T) {
		t.Parallel()

		got, err := io.ReadAll(segmentadmin.WithContext(t.Context(), strings.NewReader("abc")))
		require.NoError(t, err)
		assert.Equal(t, "abc", string(got))
	})

	t.Run("a read that blocks ends with the context", func(t *testing.T) {
		t.Parallel()

		pipeReader, pipeWriter := io.Pipe()

		t.Cleanup(func() { _ = pipeWriter.Close() })

		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)

		go func() {
			_, err := segmentadmin.WithContext(ctx, pipeReader).Read(make([]byte, 8))
			result <- err
		}()

		cancel()

		select {
		case err := <-result:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(10 * time.Second):
			require.FailNow(t, "the read did not end with the context")
		}

		_, err := segmentadmin.WithContext(ctx, pipeReader).Read(make([]byte, 8))
		require.ErrorIs(t, err, context.Canceled, "a read after the end of the context fails at once")
	})
}

func TestKeyCounter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		pages     [][]string
		limit     int
		wantCount int
		wantMore  bool
	}{
		{
			name:      "duplicates across pages count once",
			limit:     10,
			pages:     [][]string{{"a", "b"}, {"b", "c", "a"}},
			wantCount: 3,
		},
		{name: "the limit is exact", limit: 3, pages: [][]string{{"a", "b", "c"}, {"a"}}, wantCount: 3},
		{
			name:      "a key beyond the limit says more",
			limit:     2,
			pages:     [][]string{{"a", "b"}, {"c"}},
			wantCount: 2,
			wantMore:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			counter := segmentadmin.NewKeyCounter(tt.limit)

			for _, page := range tt.pages {
				if counter.Add(page) {
					break
				}
			}

			assert.Equal(t, tt.wantCount, counter.Count())
			assert.Equal(t, tt.wantMore, counter.More())
		})
	}
}
