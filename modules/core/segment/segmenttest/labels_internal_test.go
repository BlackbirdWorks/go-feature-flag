package segmenttest

import (
	"testing"
	"time"

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

// TestCaseDeadlineGrowsWithTheCalls pins that a case that makes many calls one after the other may take as many
// bounds, so a provider that is slow but timely passes.
func TestCaseDeadlineGrowsWithTheCalls(t *testing.T) {
	t.Parallel()

	const bound = 100 * time.Millisecond

	tests := []struct {
		name string
		tc   testCase
		want time.Duration
	}{
		{name: "a case with a call or two", tc: testCase{}, want: 3 * bound},
		{
			name: "the concurrent case: 25 calls one after the other for each worker",
			tc:   testCase{calls: rounds},
			want: 27 * bound,
		},
		{
			name: "the retained slice case: the rounds of two phases",
			tc:   testCase{calls: 2*rounds + 1},
			want: 53 * bound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, caseDeadline(tt.tc, bound))
		})
	}

	for _, tcase := range cases() {
		assert.GreaterOrEqual(t, caseDeadline(tcase, bound), 3*bound, tcase.name)
	}
}
