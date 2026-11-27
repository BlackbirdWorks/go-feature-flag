package manifestreader

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRightSize: a buffer with little spare capacity is returned as it is, with its capacity capped (no second
// buffer next to a body near the limit); one with much spare capacity is copied into a buffer of exactly the size.
func TestRightSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		length     int
		capacity   int
		wantShared bool
	}{
		{name: "no spare capacity", length: 100, capacity: 100, wantShared: true},
		{name: "a fifth spare", length: 100, capacity: 120, wantShared: true},
		{name: "a quarter spare", length: 100, capacity: 125, wantShared: true},
		{name: "more than a quarter spare", length: 100, capacity: 126},
		{name: "a small body in a large buffer", length: 10, capacity: 4096},
		{name: "empty", length: 0, capacity: 0, wantShared: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input := make([]byte, tt.length, tt.capacity)
			for i := range input {
				input[i] = byte(i)
			}

			out := rightSize(input)

			assert.Equal(t, input, out)
			assert.Equal(t, tt.length, cap(out), "no spare capacity is returned")

			if tt.length == 0 {
				return
			}

			assert.Equal(t, tt.wantShared, &out[0] == &input[0], "the read buffer is reused, not copied")
		})
	}
}
