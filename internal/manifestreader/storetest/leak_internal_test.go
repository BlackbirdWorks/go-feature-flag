package storetest

import (
	"context"
	"runtime/pprof"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blocked starts a goroutine that carries the labels and waits for release.
func blocked(t *testing.T, runID, caseID string) func() {
	t.Helper()

	stop := make(chan struct{})
	started := make(chan struct{})

	pprof.Do(t.Context(), pprof.Labels(labelRun, runID, labelCase, caseID), func(context.Context) {
		go func() {
			close(started)
			<-stop
		}()
	})

	<-started

	return func() { close(stop) }
}

func TestLeakedGoroutines_FindsTheLabelledOnesOnly(t *testing.T) {
	t.Parallel()

	runID := newID("leak-test")
	release := blocked(t, runID, "case-a")
	releaseOther := blocked(t, runID, "case-b")
	releaseElsewhere := blocked(t, newID("other-run"), "case-a")

	defer releaseElsewhere()

	assert.Len(t, leakedGoroutines(runID, ""), 2, "the run as a whole")
	assert.Len(t, leakedGoroutines(runID, "case-a"), 1, "one case")
	assert.Empty(t, leakedGoroutines(runID, "case-c"))

	release()
	assert.Empty(t, settledLeaks(runID, "case-a"), "the goroutine ended")
	assert.Len(t, settledLeaks(runID, ""), 1, "the other case is still there")

	releaseOther()
	assert.Empty(t, settledLeaks(runID, ""))
}

func TestExempt_OnlyTheConnectionPoolLoops(t *testing.T) {
	t.Parallel()

	tests := []struct {
		group string
		want  bool
	}{
		{
			group: "1 @ 0x1\n#\t0x2\tnet/http.(*persistConn).readLoop+0x1f\t/usr/lib/go/src/net/http/transport.go:2200",
			want:  true,
		},
		{
			group: "1 @ 0x1\n#\t0x2\tnet/http.(*persistConn).writeLoop+0x1f\t/usr/lib/go/src/net/http/transport.go:2500",
			want:  true,
		},
		{group: "1 @ 0x1\n#\t0x2\tnet/http.(*Transport).dialConnFor+0x1f\t/usr/lib/go/src/net/http/transport.go:1700"},
		{group: "1 @ 0x1\n#\t0x2\tgithub.com/x/store.(*Store).Get.func1+0x1f\t/x/store.go:10"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, exempt(tt.group), tt.group)
	}
}

func TestGroupLabels(t *testing.T) {
	t.Parallel()

	assert.Equal(t, map[string]string{"a": "b"}, groupLabels("2 @ 0x1\n# labels: {\"a\":\"b\"}\n#\t0x2\tf"))
	assert.Nil(t, groupLabels("2 @ 0x1\n#\t0x2\tf"))
	assert.Nil(t, groupLabels("2 @ 0x1\n# labels: not json\n#\t0x2\tf"))
}

func TestCheck_UnknownCaseAndUnsupportedCase(t *testing.T) {
	t.Parallel()

	failures, ran := Check("no-such-case", Fixture{})
	assert.True(t, ran)
	assert.Equal(t, []string{"no such case: no-such-case"}, failures)

	failures, ran = Check("denied-is-not-retryable", Fixture{Unsupported: NewWaivers().With(CapDeny, "no")})
	assert.False(t, ran)
	assert.Nil(t, failures)
}

func TestUnsupportedReason(t *testing.T) {
	t.Parallel()

	fixture := &Fixture{Unsupported: NewWaivers().With(CapHang, "a").With(CapDeny, "b")}

	require.Empty(t, unsupportedReason(fixture, nil))
	require.Empty(t, unsupportedReason(fixture, []Capability{CapThrottle}))
	assert.Equal(t, "hang: a", unsupportedReason(fixture, []Capability{CapHang}))
	assert.Equal(t, "hang: a; deny: b", unsupportedReason(fixture, []Capability{CapHang, CapDeny}))
}

func TestHang(t *testing.T) {
	t.Parallel()

	hang := NewHang()
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)

	go func() { done <- hang.Block(ctx) }()

	<-hang.Entered()
	assert.Equal(t, 1, hang.Active())

	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	assert.Equal(t, 0, hang.Active())

	hang.Release()
	hang.Release() // twice is fine
	require.NoError(t, hang.Block(t.Context()))
}

func TestCheckWaivers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		waivers     Waivers
		declared    Waivers
		name        string
		wantFailure bool
	}{
		{name: "none and none", waivers: nil, declared: nil},
		{name: "none and an empty map", waivers: nil, declared: NewWaivers()},
		{name: "the same", waivers: NewWaivers().With(CapHang, "a"), declared: NewWaivers().With(CapHang, "a")},
		{
			name:        "a waiver the test did not state",
			waivers:     nil,
			declared:    NewWaivers().With(CapHang, "a"),
			wantFailure: true,
		},
		{
			name:        "a waiver the fixture dropped",
			waivers:     NewWaivers().With(CapHang, "a"),
			declared:    nil,
			wantFailure: true,
		},
		{
			name:    "another reason",
			waivers: NewWaivers().With(CapHang, "a"), declared: NewWaivers().With(CapHang, "b"), wantFailure: true,
		},
		{
			name:    "another capability",
			waivers: NewWaivers().With(CapHang, "a"), declared: NewWaivers().With(CapDeny, "a"), wantFailure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := newRecorder()
			defer rec.stop()

			checkWaivers(rec, tt.waivers, &Fixture{Unsupported: tt.declared})
			assert.Equal(t, tt.wantFailure, len(rec.failures()) > 0, "%v", rec.failures())
		})
	}
}
