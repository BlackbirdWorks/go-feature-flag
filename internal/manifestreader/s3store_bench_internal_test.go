//go:build bench

package manifestreader

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

// BenchmarkReadBody measures the body path of the S3 store for a small body and a large limit, with and without
// a Content-Length.
func BenchmarkReadBody(b *testing.B) {
	const limit = 16 << 20

	body := strings.Repeat("x", 4<<10)
	length := int64(len(body))

	b.Run("content-length", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_, _ = readBody(bytes.NewReader([]byte(body)), &length, limit)
		}
	})

	b.Run("no-content-length", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_, _ = readBody(io.NopCloser(strings.NewReader(body)), nil, limit)
		}
	})
}
