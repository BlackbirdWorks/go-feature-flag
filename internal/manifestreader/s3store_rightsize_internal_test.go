package manifestreader

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// scriptedBody serves its steps one Read at a time: bytes and the error that goes with them.
type scriptedBody struct {
	steps []bodyStep
}

type bodyStep struct {
	err  error
	data string
}

func (s *scriptedBody) Read(buf []byte) (int, error) {
	if len(s.steps) == 0 {
		return 0, io.EOF
	}

	step := s.steps[0]
	s.steps = s.steps[1:]

	copied := copy(buf, step.data)

	return copied, step.err
}

// TestReadBody_LimitBeforeTransferError: a body of unknown length that gave more than limit bytes is too large,
// whether the transfer error came in the same Read as the extra byte or after it; an error before the limit is a
// retryable transfer error.
func TestReadBody_LimitBeforeTransferError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantIs error
		name   string
		steps  []bodyStep
		limit  int64
	}{
		{
			name: "the extra byte and an error in one read", limit: 4,
			steps: []bodyStep{{data: "12345", err: errDisk}}, wantIs: ErrTooLarge,
		},
		{
			name: "the extra byte, then an error", limit: 4,
			steps: []bodyStep{{data: "12345"}, {err: errDisk}}, wantIs: ErrTooLarge,
		},
		{
			name: "an error before the limit", limit: 4,
			steps: []bodyStep{{data: "12", err: errDisk}}, wantIs: errDisk,
		},
		{
			name: "the limit exactly, then an error", limit: 4,
			steps: []bodyStep{{data: "1234", err: errDisk}}, wantIs: errDisk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := readBody(&scriptedBody{steps: tt.steps}, nil, tt.limit)
			require.ErrorIs(t, err, tt.wantIs)
			assert.Nil(t, got)
		})
	}
}
