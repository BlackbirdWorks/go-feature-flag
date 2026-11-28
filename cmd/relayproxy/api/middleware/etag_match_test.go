package middleware_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
)

func TestETagMatches(t *testing.T) {
	t.Parallel()

	const tag = `"12-abc"`

	tests := []struct {
		name        string
		ifNoneMatch string
		etag        string
		want        bool
	}{
		{name: "same strong tag", ifNoneMatch: tag, etag: tag, want: true},
		{name: "weak request tag", ifNoneMatch: "W/" + tag, etag: tag, want: true},
		{name: "weak response tag", ifNoneMatch: tag, etag: "W/" + tag, want: true},
		{name: "list, tag last", ifNoneMatch: `"a", ` + tag, etag: tag, want: true},
		{name: "list, tag in the middle without spaces", ifNoneMatch: `"a",` + tag + `,"b"`, etag: tag, want: true},
		{name: "list, weak tag among others", ifNoneMatch: `"a" , W/` + tag, etag: tag, want: true},
		{name: "star", ifNoneMatch: "*", etag: tag, want: true},
		{name: "star in a list", ifNoneMatch: `"a", *`, etag: tag, want: true},
		{name: "other tag", ifNoneMatch: `"other"`, etag: tag, want: false},
		{name: "list without the tag", ifNoneMatch: `"a", "b"`, etag: tag, want: false},
		{name: "tag without quotes", ifNoneMatch: "12-abc", etag: tag, want: false},
		{name: "empty header", ifNoneMatch: "", etag: tag, want: false},
		{name: "empty items", ifNoneMatch: ",,", etag: tag, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, middleware.ETagMatches(tt.ifNoneMatch, tt.etag))
		})
	}
}
