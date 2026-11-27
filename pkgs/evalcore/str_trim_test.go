package evalcore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

const (
	helloworld = "hello world"
)

func TestStrTrim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single line with leading spaces",
			input: "   hello",
			want:  "hello",
		},
		{
			name:  "multiple lines with leading spaces",
			input: "   hello\n   world",
			want:  helloworld,
		},
		{
			name:  "no leading spaces",
			input: "hello\nworld",
			want:  helloworld,
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only spaces",
			input: "   ",
			want:  "",
		},
		{
			name:  "line that ends with a space is joined as it is",
			input: "hello \n  world",
			want:  helloworld,
		},
		{
			name:  "CRLF line ends and blank lines",
			input: "\r\n hello\r\n\r\nworld\r\n\n",
			want:  helloworld,
		},
		{
			name:  "brackets and commas take no separator",
			input: "[\n1,\n2\n]",
			want:  "[1, 2]",
		},
		{
			name:  "mixed leading spaces",
			input: "   hello\nworld",
			want:  helloworld,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := evalcore.StrTrim(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
