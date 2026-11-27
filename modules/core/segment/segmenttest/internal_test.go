package segmenttest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestCountLabelled pins the count of the goroutines of a case in a profile: a header stands for as many goroutines as
// it says, and the labels of a goroutine can hold other labels besides ours.
func TestCountLabelled(t *testing.T) {
	t.Parallel()

	const dump = `goroutine profile: total 9
3 @ 0x1 0x2
# labels: {"segmenttest":"case-a"}
#	0x1	pkg.f+0x0	file.go:1

1 @ 0x3
# labels: {"other":"x", "segmenttest":"case-a"}
#	0x3	pkg.g+0x0	file.go:2

2 @ 0x4
# labels: {"segmenttest":"case-b"}
#	0x4	pkg.h+0x0	file.go:3

2 @ 0x5
#	0x5	pkg.i+0x0	file.go:4
`

	tests := []struct {
		name  string
		label string
		want  int
	}{
		{name: "several goroutines in one header, and one with another label", label: "case-a", want: 4},
		{name: "another case", label: "case-b", want: 2},
		{name: "a case that left nothing", label: "case-c", want: 0},
		{name: "a label that is only the start of another", label: "case", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, countLabelled(dump, tt.label))
		})
	}
}
