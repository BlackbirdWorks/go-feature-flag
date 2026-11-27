package valkeysegment_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	replyOne        = ":1\r\n"
	segBeta         = "beta"
	segGhost        = "ghost"
	segFlags        = "flags"
	keyBeta         = "seg:{beta}"
	flagPrefix      = "flag:"
	idAlice         = "alice"
	idCarol         = "carol"
	msgInitProtocol = "valkey segment: init failed: valkey segment: unexpected reply"
	segNope         = "nope"
	msgProtocol2    = "valkey segment: protocol: 2 of 2 lookups failed"
	callTimeout     = 5 * time.Second
)

func lk(seg, id string) segment.Lookup { return segment.Lookup{Segment: seg, ID: id} }

func states(lookups []segment.Lookup) []segment.State {
	out := make([]segment.State, len(lookups))
	for i := range lookups {
		out[i] = lookups[i].State
	}

	return out
}

// loaded is a stub server that holds the set beta (alice, carol) and the member key flag:alice.
func loaded(tb testing.TB) *stubServer {
	tb.Helper()

	srv := newStubServer(tb)
	srv.setMembers(keyBeta, idAlice, idCarol)
	srv.setKey(flagPrefix + idAlice)

	return srv
}

func baseOptions(counters *valkeysegment.Counters) *valkeysegment.Options {
	opts := &valkeysegment.Options{
		Timeout: callTimeout,
		Segments: map[string]valkeysegment.SegmentOptions{
			segBeta:  {Key: keyBeta},
			segGhost: {Key: "seg:{ghost}"},
			segFlags: {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
		},
	}

	if counters != nil { // a nil *Counters in the interface would not be a nil Observer
		opts.Observer = counters
	}

	return opts
}

func newProvider(tb testing.TB, srv *stubServer, opts *valkeysegment.Options) *valkeysegment.Provider {
	tb.Helper()

	prov, err := valkeysegment.Dial(srv.clientOption(), opts)
	require.NoError(tb, err)
	tb.Cleanup(func() {
		srv.Release()

		ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
		defer cancel()

		assert.NoError(tb, prov.Shutdown(ctx))
	})

	return prov
}

func TestContains(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", valkeysegment.DefaultMaxIDBytes+1)

	tests := []struct {
		name          string
		opts          func(*valkeysegment.Options)
		lookups       []segment.Lookup
		wantStates    []segment.State
		wantAnswered  uint64
		wantOversized uint64
		wantReady     bool
	}{
		{
			name:         "member and not member",
			lookups:      []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed"), lk(segBeta, idCarol)},
			wantStates:   []segment.State{segment.Member, segment.NotMember, segment.Member},
			wantAnswered: 3,
			wantReady:    true,
		},
		{
			name:         "missing set key is unknown",
			lookups:      []segment.Lookup{lk(segGhost, idAlice)},
			wantStates:   []segment.State{segment.Unknown},
			wantAnswered: 0,
			wantReady:    true,
		},
		{
			name:         "missing set key is not member on request",
			opts:         func(o *valkeysegment.Options) { o.MissingKey = valkeysegment.MissingNotMember },
			lookups:      []segment.Lookup{lk(segGhost, idAlice), lk(segBeta, idAlice)},
			wantStates:   []segment.State{segment.NotMember, segment.Member},
			wantAnswered: 2,
			wantReady:    true,
		},
		{
			name:         nameKeysLayout,
			lookups:      []segment.Lookup{lk(segFlags, idAlice), lk(segFlags, "zed"), lk(segFlags, "")},
			wantStates:   []segment.State{segment.Member, segment.NotMember, segment.Unknown},
			wantAnswered: 2,
			wantReady:    true,
		},
		{
			name:         "unknown segment is never sent",
			lookups:      []segment.Lookup{lk(segNope, idAlice), lk(segBeta, idAlice)},
			wantStates:   []segment.State{segment.Unknown, segment.Member},
			wantAnswered: 1,
			wantReady:    true,
		},
		{
			name:          "an over-long id stays unknown and is counted",
			lookups:       []segment.Lookup{lk(segBeta, long), lk(segFlags, long), lk(segBeta, idAlice)},
			wantStates:    []segment.State{segment.Unknown, segment.Unknown, segment.Member},
			wantAnswered:  1,
			wantReady:     true,
			wantOversized: 2,
		},
		{
			name:         "an id of exactly the limit is looked up",
			lookups:      []segment.Lookup{lk(segBeta, strings.Repeat("x", valkeysegment.DefaultMaxIDBytes))},
			wantStates:   []segment.State{segment.NotMember},
			wantAnswered: 1,
			wantReady:    true,
		},
		{
			name: "set and keys in one call keep their positions",
			lookups: []segment.Lookup{
				lk(segFlags, idAlice),
				lk(segBeta, idCarol),
				lk(segBeta, "x"),
				lk(segFlags, idCarol),
			},
			wantStates:   []segment.State{segment.Member, segment.Member, segment.NotMember, segment.NotMember},
			wantAnswered: 4,
			wantReady:    true,
		},
		{
			name:         "a prefilled state does not survive",
			lookups:      []segment.Lookup{{Segment: "nope", ID: idAlice, State: segment.Member}},
			wantStates:   []segment.State{segment.Unknown},
			wantAnswered: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			counters := &valkeysegment.Counters{}
			opts := baseOptions(counters)

			if tt.opts != nil {
				tt.opts(opts)
			}

			prov := newProvider(t, srv, opts)

			require.NoError(t, prov.Contains(context.Background(), tt.lookups))
			assert.Equal(t, tt.wantStates, states(tt.lookups))

			stats := counters.Snapshot()
			assert.Equal(t, tt.wantAnswered, stats.Answered)
			assert.Equal(t, tt.wantOversized, stats.Oversized)
			assert.Equal(t, uint64(len(tt.lookups))-tt.wantAnswered, stats.Unknown)
			assert.Empty(t, stats.Failures)
			assert.Equal(t, valkeysegment.Status{Ready: tt.wantReady}, prov.Status())
			assert.Equal(t, segment.Health{Ready: tt.wantReady}, prov.Health())
		})
	}
}

func TestContainsSplitsLargeSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		total        int
		wantCommands int
	}{
		{name: "exactly the limit is one command", total: valkeysegment.MaxIDsPerCommand, wantCommands: 1},
		{name: "one over the limit is two", total: valkeysegment.MaxIDsPerCommand + 1, wantCommands: 2},
		{name: "several commands", total: 2*valkeysegment.MaxIDsPerCommand + 500, wantCommands: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newStubServer(t)
			members := make([]string, 0, tt.total/2)
			lookups := make([]segment.Lookup, tt.total)
			want := make([]segment.State, tt.total)

			for i := range tt.total {
				id := "m" + strconv.Itoa(i)
				lookups[i] = lk(segBeta, id)
				want[i] = segment.NotMember

				if i%2 == 0 {
					members = append(members, id)
					want[i] = segment.Member
				}
			}

			srv.setMembers(keyBeta, members...)

			prov := newProvider(t, srv, baseOptions(nil))

			require.NoError(t, prov.Init(context.Background())) // the first use of a connection writes its own setup

			before := srv.writes.Load()
			existsBefore := srv.count("exists")
			smisBefore := srv.count("smismember")

			require.NoError(t, prov.Contains(context.Background(), lookups))
			assert.Equal(t, want, states(lookups))
			assert.Equal(t, tt.wantCommands, srv.count("smismember")-smisBefore, "ids per command are capped")
			assert.Equal(t, tt.wantCommands, srv.count("exists")-existsBefore, "one key check for each command")
			assert.Equal(t, int64(1), srv.writes.Load()-before, "all the commands of the call are one write")
		})
	}
}

func TestContainsIsOneRoundTrip(t *testing.T) {
	t.Parallel()

	srv := loaded(t)
	counters := &valkeysegment.Counters{}
	prov := newProvider(t, srv, baseOptions(counters))

	require.NoError(t, prov.Init(context.Background())) // the first use of a connection writes its own setup

	before := srv.writes.Load()
	existsBefore := srv.count("exists")
	smisBefore := srv.count("smismember")
	lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segFlags, idAlice), lk(segFlags, "zed"), lk(segGhost, idAlice)}

	require.NoError(t, prov.Contains(context.Background(), lookups))
	assert.Equal(t, int64(1), srv.writes.Load()-before, "one write for the whole call")
	assert.Equal(t, 2, srv.count("smismember")-smisBefore)
	assert.Equal(t, 4, srv.count("exists")-existsBefore, "two key checks of the sets and two member keys")
	assert.Equal(t, uint64(1), counters.Snapshot().RoundTrips)
}

func TestContainsEmptyAndClosed(t *testing.T) {
	t.Parallel()

	srv := loaded(t)
	counters := &valkeysegment.Counters{}
	prov := newProvider(t, srv, baseOptions(counters))

	require.NoError(t, prov.Contains(context.Background(), nil))
	assert.Equal(t, uint64(0), counters.Snapshot().Calls, "an empty call is not observed")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
	defer cancel()

	require.NoError(t, prov.Shutdown(ctx))
	require.NoError(t, prov.Shutdown(ctx), "a second Shutdown is a no-op")

	lookups := []segment.Lookup{{Segment: segBeta, ID: idAlice, State: segment.Member}}

	require.ErrorIs(t, prov.Contains(context.Background(), lookups), valkeysegment.ErrClosed)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
	require.ErrorIs(t, prov.Init(context.Background()), valkeysegment.ErrClosed)

	_, err := prov.Exists(context.Background(), []string{segBeta})
	require.ErrorIs(t, err, valkeysegment.ErrClosed)
	assert.Equal(t, valkeysegment.Status{Closed: true}, prov.Status())
	assert.Equal(t, uint64(0), counters.Snapshot().Calls)
}

func TestContainsFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arrange     func(*stubServer)
		wantSentry  error
		name        string
		wantMessage string
		lookups     []segment.Lookup
		wantStates  []segment.State
		wantFailed  int
		wantClass   valkeysegment.Class
	}{
		{
			name:        "server down",
			arrange:     func(s *stubServer) { s.Fail() },
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segFlags, idAlice)},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassUnavailable,
			wantSentry:  valkeysegment.ErrUnavailable,
			wantFailed:  2,
			wantMessage: "valkey segment: unavailable: 2 of 2 lookups failed",
		},
		{
			name:        "one segment answers an error",
			arrange:     func(s *stubServer) { s.FailSegment("flag:") },
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segFlags, idAlice), lk(segFlags, "zed")},
			wantStates:  []segment.State{segment.Member, segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassServer,
			wantSentry:  valkeysegment.ErrServer,
			wantFailed:  2,
			wantMessage: "valkey segment: server: 2 of 3 lookups failed",
		},
		{
			name:        "the key check of a set fails",
			arrange:     func(s *stubServer) { s.refuse("exists") },
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   valkeysegment.ClassServer,
			wantSentry:  valkeysegment.ErrServer,
			wantFailed:  1,
			wantMessage: "valkey segment: server: 1 of 2 lookups failed",
		},
		{
			name:        "a reply of the wrong type",
			arrange:     func(s *stubServer) { s.answer(replyOK) },
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segFlags, idAlice)},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassProtocol,
			wantSentry:  valkeysegment.ErrProtocol,
			wantFailed:  2,
			wantMessage: msgProtocol2,
		},
		{
			name:        "a count that is not 0 or 1",
			arrange:     func(s *stubServer) { s.answer(":7\r\n") },
			lookups:     []segment.Lookup{lk(segFlags, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   valkeysegment.ClassProtocol,
			wantSentry:  valkeysegment.ErrProtocol,
			wantFailed:  1,
			wantMessage: "valkey segment: protocol: 1 of 1 lookups failed",
		},
		{
			name:        "a list with a value that is not 0 or 1",
			arrange:     func(s *stubServer) { s.answer("*2\r\n:7\r\n:1\r\n") },
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, idCarol)},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassProtocol,
			wantSentry:  valkeysegment.ErrProtocol,
			wantFailed:  2,
			wantMessage: msgProtocol2,
		},
		{
			name:        "a list of the wrong length",
			arrange:     func(s *stubServer) { s.answer("*1\r\n:1\r\n") },
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, idCarol)},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassProtocol,
			wantSentry:  valkeysegment.ErrProtocol,
			wantFailed:  2,
			wantMessage: msgProtocol2,
		},
		{
			name:        "a stream that does not parse",
			arrange:     func(s *stubServer) { s.answer("?garbage\r\n") },
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segFlags, idAlice)},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   valkeysegment.ClassProtocol,
			wantSentry:  valkeysegment.ErrProtocol,
			wantFailed:  2,
			wantMessage: msgProtocol2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			counters := &valkeysegment.Counters{}
			prov := newProvider(t, srv, baseOptions(counters))

			tt.arrange(srv)

			err := prov.Contains(context.Background(), tt.lookups)

			var lerr *valkeysegment.LookupError

			require.ErrorAs(t, err, &lerr)
			require.ErrorIs(t, err, tt.wantSentry)
			assert.Equal(t, tt.wantClass, lerr.Class)
			assert.Equal(t, tt.wantFailed, lerr.Failed)
			assert.Equal(t, len(tt.lookups), lerr.Total)
			assert.Equal(t, tt.wantMessage, err.Error())
			assert.Equal(t, tt.wantStates, states(tt.lookups))
			assert.Equal(t, map[string]uint64{tt.wantClass.String(): 1}, counters.Snapshot().Failures)
			assert.Equal(t, valkeysegment.Status{LastFailure: tt.wantClass}, prov.Status())
			assert.Equal(t, segment.Health{Failure: tt.wantClass.String()}, prov.Health())
		})
	}
}

func TestContainsRecoversAfterFailure(t *testing.T) {
	t.Parallel()

	srv := loaded(t)
	prov := newProvider(t, srv, baseOptions(nil))
	srv.Fail()

	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	require.ErrorIs(t, prov.Contains(context.Background(), lookups), valkeysegment.ErrUnavailable)

	srv.Heal()

	require.NoError(t, prov.Contains(context.Background(), lookups))
	assert.Equal(t, []segment.State{segment.Member}, states(lookups))
	assert.Equal(t, valkeysegment.Status{Ready: true}, prov.Status())
}

func TestContainsContext(t *testing.T) {
	t.Parallel()

	t.Run("a context that ended before is not sent", func(t *testing.T) {
		t.Parallel()

		srv := loaded(t)
		prov := newProvider(t, srv, baseOptions(nil))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		lookups := []segment.Lookup{lk(segBeta, idAlice)}
		err := prov.Contains(ctx, lookups)

		require.ErrorIs(t, err, valkeysegment.ErrCanceled)
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
		assert.Equal(t, 0, srv.count("smismember"))
	})

	t.Run("the call timeout ends a hung call", func(t *testing.T) {
		t.Parallel()

		srv := loaded(t)
		opts := baseOptions(nil)
		opts.Timeout = 100 * time.Millisecond
		prov := newProvider(t, srv, opts)
		entered := srv.Hang()

		lookups := []segment.Lookup{lk(segBeta, idAlice)}
		err := prov.Contains(context.Background(), lookups)

		require.ErrorIs(t, err, valkeysegment.ErrTimeout)
		assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
		assert.NotEmpty(t, entered, "the server got the command")
		assert.Equal(t, valkeysegment.Status{LastFailure: valkeysegment.ClassTimeout}, prov.Status())
	})

	cancelTests := []struct {
		name    string
		lookups int
	}{
		{name: "the cancel of the caller ends a hung call", lookups: 1},
		{name: "the cancel ends a batch above the blocking pipeline threshold of valkey-go", lookups: 2100},
	}

	for _, tt := range cancelTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			opts := baseOptions(nil)
			opts.Timeout = time.Hour // only the cancel can end the call
			prov := newProvider(t, srv, opts)
			entered := srv.Hang()
			ctx, cancel := context.WithCancel(context.Background())
			errs := make(chan error, 1)
			lookups := make([]segment.Lookup, tt.lookups)

			for i := range lookups {
				lookups[i] = lk(segFlags, "id"+strconv.Itoa(i))
			}

			go func() { errs <- prov.Contains(ctx, lookups) }()

			<-entered
			cancel()

			select {
			case err := <-errs:
				require.ErrorIs(t, err, valkeysegment.ErrCanceled)
			case <-time.After(callTimeout):
				assert.Fail(t, "the call did not end after the cancel")
			}
		})
	}
}

func TestShutdownIsBounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		name       string
		pipelining valkeysegment.Pipelining
	}{
		{
			name:       "always ends the call in flight as canceled",
			pipelining: valkeysegment.PipeliningAlways,
			wantErr:    valkeysegment.ErrCanceled,
		},
		{
			name:       "auto ends the call in flight when the client closes",
			pipelining: valkeysegment.PipeliningAuto,
			wantErr:    valkeysegment.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			opts := baseOptions(nil)
			opts.Pipelining = tt.pipelining
			prov := newProvider(t, srv, opts)
			entered := srv.Hang()
			errs := make(chan error, 1)

			go func() { errs <- prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}) }()

			select {
			case <-entered:
			case err := <-errs:
				require.Fail(t, "the call ended before the server got the command", "%v", err)
			case <-time.After(callTimeout):
				require.Fail(t, "the server did not get the command")
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // Shutdown has no time to wait

			require.ErrorIs(t, prov.Shutdown(ctx), context.Canceled, "Shutdown gives up when its context ends")

			select {
			case err := <-errs:
				require.ErrorIs(t, err, tt.wantErr, "the call in flight was ended by Shutdown")
			case <-time.After(callTimeout):
				assert.Fail(t, "the call in flight was not ended by Shutdown")
			}

			// after a Shutdown that timed out the provider is closed for every later call
			lookups := []segment.Lookup{lk(segBeta, idAlice)}

			require.ErrorIs(t, prov.Contains(context.Background(), lookups), valkeysegment.ErrClosed)
			require.ErrorIs(t, prov.Init(context.Background()), valkeysegment.ErrClosed)
		})
	}
}

func TestExists(t *testing.T) {
	t.Parallel()

	srv := loaded(t)
	prov := newProvider(t, srv, baseOptions(nil))

	got, err := prov.Exists(context.Background(), []string{segBeta, segGhost, segFlags, segNope})
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Member, segment.NotMember, segment.Unknown, segment.Unknown}, got)

	got, err = prov.Exists(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = prov.Exists(context.Background(), []string{segFlags})
	require.NoError(t, err)
	assert.Equal(t, []segment.State{segment.Unknown}, got)

	srv.FailSegment("ghost")

	got, err = prov.Exists(context.Background(), []string{segBeta, segGhost})

	var lerr *valkeysegment.LookupError

	require.ErrorAs(t, err, &lerr)
	assert.Equal(t, &valkeysegment.LookupError{Class: valkeysegment.ClassServer, Failed: 1, Total: 2}, lerr)
	assert.Equal(t, []segment.State{segment.Member, segment.Unknown}, got)

	srv.Heal()
	srv.Fail()

	got, err = prov.Exists(context.Background(), []string{segBeta})
	require.ErrorIs(t, err, valkeysegment.ErrUnavailable)
	assert.Equal(t, []segment.State{segment.Unknown}, got)

	srv.Heal()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = prov.Exists(ctx, []string{segBeta})
	require.ErrorIs(t, err, valkeysegment.ErrCanceled)

	srv.answer(replyOK)

	_, err = prov.Exists(context.Background(), []string{segBeta})
	require.ErrorIs(t, err, valkeysegment.ErrProtocol)
}

func TestInit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		arrange       func(*stubServer)
		opts          func(*valkeysegment.Options)
		wantErr       error
		wantMessage   string
		wantSMIS      int
		wantExists    int
		wantReadyFlag bool
	}{
		{
			name:          "a store that has the commands",
			wantSMIS:      1,
			wantExists:    2,
			wantReadyFlag: true,
		},
		{
			name:          "a missing key is not checked when it is a non-member",
			opts:          func(o *valkeysegment.Options) { o.MissingKey = valkeysegment.MissingNotMember },
			wantSMIS:      1,
			wantExists:    1,
			wantReadyFlag: true,
		},
		{
			name:    "SMISMEMBER refused",
			arrange: func(s *stubServer) { s.refuse("smismember") },
			wantErr: valkeysegment.ErrServer,
			wantMessage: "valkey segment: init failed: the command SMISMEMBER is not available: " +
				"valkey segment: store returned an error",
			wantSMIS:   1,
			wantExists: 2,
		},
		{
			name:        "store down",
			arrange:     func(s *stubServer) { s.Fail() },
			wantErr:     valkeysegment.ErrUnavailable,
			wantMessage: "valkey segment: init failed: valkey segment: store unavailable",
			wantSMIS:    0,
			wantExists:  0,
		},
		{
			name:        "a probe answers an OK",
			arrange:     func(s *stubServer) { s.answer(replyOK) },
			wantErr:     valkeysegment.ErrProtocol,
			wantMessage: msgInitProtocol,
			wantSMIS:    1,
			wantExists:  2,
		},
		{
			name:        "a probe answers a bulk string",
			arrange:     func(s *stubServer) { s.answer("$3\r\nfoo\r\n") },
			wantErr:     valkeysegment.ErrProtocol,
			wantMessage: msgInitProtocol,
			wantSMIS:    1,
			wantExists:  2,
		},
		{
			name:        "a probe answers a list of the wrong length",
			arrange:     func(s *stubServer) { s.answer("*2\r\n:0\r\n:0\r\n") },
			wantErr:     valkeysegment.ErrProtocol,
			wantMessage: msgInitProtocol,
			wantSMIS:    1,
			wantExists:  2,
		},
		{
			name:        "a probe answers a nil",
			arrange:     func(s *stubServer) { s.answer("_\r\n") },
			wantErr:     valkeysegment.ErrProtocol,
			wantMessage: msgInitProtocol,
			wantSMIS:    1,
			wantExists:  2,
		},
		{
			name:        "EXISTS answers a count that is not 0 or 1",
			arrange:     func(s *stubServer) { s.answerOnly("exists", ":7\r\n") },
			wantErr:     valkeysegment.ErrProtocol,
			wantMessage: msgInitProtocol,
			wantSMIS:    1,
			wantExists:  2,
		},
		{
			name:        "EXISTS answers a bulk string",
			arrange:     func(s *stubServer) { s.answerOnly("exists", "$1\r\n1\r\n") },
			wantErr:     valkeysegment.ErrProtocol,
			wantMessage: msgInitProtocol,
			wantSMIS:    1,
			wantExists:  2,
		},
		{
			name:        "ping answers something else",
			arrange:     func(s *stubServer) { s.pongWith("+NOPE\r\n") },
			wantErr:     valkeysegment.ErrProtocol,
			wantMessage: msgInitProtocol,
			wantSMIS:    0,
			wantExists:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			opts := baseOptions(nil)
			opts.Segments = map[string]valkeysegment.SegmentOptions{
				segBeta: {Key: keyBeta}, segFlags: {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
			}

			if tt.opts != nil {
				tt.opts(opts)
			}

			prov := newProvider(t, srv, opts)

			if tt.arrange != nil {
				tt.arrange(srv)
			}

			err := prov.Init(context.Background())

			if tt.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorIs(t, err, valkeysegment.ErrInit)
				assert.Equal(t, tt.wantMessage, err.Error())
			}

			assert.Equal(t, tt.wantReadyFlag, prov.Status().Ready)
			assert.Equal(t, tt.wantSMIS, srv.count("smismember"))
			assert.Equal(t, tt.wantExists, srv.count("exists"))
		})
	}
}

func TestInitCapsTheProbes(t *testing.T) {
	t.Parallel()

	srv := newStubServer(t)
	segs := map[string]valkeysegment.SegmentOptions{}

	for i := range 2 * valkeysegment.MaxInitProbes {
		segs["s"+strconv.Itoa(i)] = valkeysegment.SegmentOptions{Key: "ns" + strconv.Itoa(i) + ":{x}"}
	}

	// two segments of one namespace are probed once
	segs["twin"] = valkeysegment.SegmentOptions{Key: "ns0:{y}"}

	prov := newProvider(
		t,
		srv,
		&valkeysegment.Options{Segments: segs, Timeout: callTimeout, MissingKey: valkeysegment.MissingNotMember},
	)

	require.NoError(t, prov.Init(context.Background()))
	assert.Equal(t, valkeysegment.MaxInitProbes, srv.count("smismember"))
	assert.Equal(t, 1, srv.count("ping"))
}

func TestInitCanceled(t *testing.T) {
	t.Parallel()

	prov := newProvider(t, loaded(t), baseOptions(nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := prov.Init(ctx)
	require.ErrorIs(t, err, valkeysegment.ErrInit)
	require.ErrorIs(t, err, valkeysegment.ErrCanceled)
}

func TestNewAndDial(t *testing.T) {
	t.Parallel()

	_, err := valkeysegment.New(nil, nil)
	require.ErrorIs(t, err, valkeysegment.ErrNilClient)

	_, err = valkeysegment.Dial(nil, nil)
	require.ErrorIs(t, err, valkeysegment.ErrNilClient)

	srv := loaded(t)

	_, err = valkeysegment.Dial(srv.clientOption(), &valkeysegment.Options{Timeout: -1})
	require.ErrorIs(t, err, valkeysegment.ErrInvalidOptions)

	opt := srv.clientOption()
	srv.stop()

	_, err = valkeysegment.Dial(opt, nil)
	require.ErrorIs(t, err, valkeysegment.ErrInit)
	require.ErrorIs(t, err, valkeysegment.ErrUnavailable)
	assert.NotContains(t, err.Error(), srv.addr(), "an error never holds the address")
}

func TestOptionsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    *valkeysegment.Options
		wantErr string
	}{
		{name: "defaults", opts: &valkeysegment.Options{}},
		{name: "negative timeout", opts: &valkeysegment.Options{Timeout: -1}, wantErr: "timeout is negative"},
		{name: "negative max id", opts: &valkeysegment.Options{MaxIDBytes: -1}, wantErr: "max id bytes is negative"},
		{
			name:    "bad missing key",
			opts:    &valkeysegment.Options{MissingKey: "x"},
			wantErr: `must be unknown or notMember, got "x"`,
		},
		{
			name:    "bad segment name",
			opts:    &valkeysegment.Options{Segments: map[string]valkeysegment.SegmentOptions{"a b": {}}},
			wantErr: `invalid segment name: "a b"`,
		},
		{
			name:    "bad layout",
			opts:    &valkeysegment.Options{Segments: map[string]valkeysegment.SegmentOptions{"a": {Layout: "x"}}},
			wantErr: `"a": layout must be set or keys, got "x"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov, err := valkeysegment.New(&fakeClient{}, tt.opts)

			if tt.wantErr == "" {
				require.NoError(t, err)
				assert.NotNil(t, prov)

				return
			}

			require.ErrorIs(t, err, valkeysegment.ErrInvalidOptions)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDialDoesNotUseCachingOrRetryByDefault(t *testing.T) {
	t.Parallel()

	srv := loaded(t)
	opts := baseOptions(nil)
	opts.Timeout = time.Second
	prov := newProvider(t, srv, opts)

	require.NoError(t, prov.Init(context.Background()))
	assert.Zero(t, srv.count("client tracking"), "no client-side caching")

	before := srv.count("exists")

	srv.Fail()

	lookups := []segment.Lookup{lk(segBeta, idAlice)}
	require.ErrorIs(t, prov.Contains(context.Background(), lookups), valkeysegment.ErrUnavailable)
	assert.Equal(
		t,
		1,
		srv.count("exists")-before,
		"a failed read is not sent again (the key check is the first command)",
	)
}

func TestDialValidatesTheClientSettingsAndNewIgnoresThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr string
		opts    valkeysegment.Options
	}{
		{
			name:    "bad pipelining",
			opts:    valkeysegment.Options{Pipelining: "off"},
			wantErr: `pipelining must be always or auto, got "off"`,
		},
		{
			name:    "negative blocking pipeline",
			opts:    valkeysegment.Options{BlockingPipeline: -1},
			wantErr: "blocking pipeline is negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := valkeysegment.Dial(loaded(t).clientOption(), &tt.opts)

			require.ErrorIs(t, err, valkeysegment.ErrInvalidOptions)
			assert.Contains(t, err.Error(), tt.wantErr)

			prov, newErr := valkeysegment.New(&fakeClient{}, &tt.opts)

			require.NoError(t, newErr, "a client that you pass is not configured by these fields")
			assert.NotNil(t, prov)
		})
	}
}

func TestContainsTrustsNothingOfABatchWithAWrongShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		replies    []string
		lookups    []segment.Lookup
		wantStates []segment.State
		wantFailed int
	}{
		{
			name:       "EXISTS 1 then 7 keeps neither",
			replies:    []string{replyOne, ":7\r\n"},
			lookups:    []segment.Lookup{lk(segFlags, idAlice), lk(segFlags, "zed")},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
			wantFailed: 2,
		},
		{
			name:       "EXISTS as a bulk string is not an integer",
			replies:    []string{"$1\r\n1\r\n"},
			lookups:    []segment.Lookup{lk(segFlags, idAlice)},
			wantStates: []segment.State{segment.Unknown},
			wantFailed: 1,
		},
		{
			name:       "a null element of SMISMEMBER is not a 0",
			replies:    []string{replyOne, "*1\r\n_\r\n"},
			lookups:    []segment.Lookup{lk(segBeta, "zed")},
			wantStates: []segment.State{segment.Unknown},
			wantFailed: 1,
		},
		{
			name:       "a bulk string element of SMISMEMBER is not an integer",
			replies:    []string{replyOne, "*1\r\n$1\r\n1\r\n"},
			lookups:    []segment.Lookup{lk(segBeta, idAlice)},
			wantStates: []segment.State{segment.Unknown},
			wantFailed: 1,
		},
		{
			name:       "a good SMISMEMBER next to a bad EXISTS keeps neither",
			replies:    []string{replyOne, "*1\r\n:1\r\n", ":7\r\n"},
			lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segFlags, idAlice)},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
			wantFailed: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			prov := newProvider(t, srv, baseOptions(nil))

			require.NoError(t, prov.Init(context.Background()))
			srv.answerSequence(tt.replies...)

			err := prov.Contains(context.Background(), tt.lookups)

			assert.Equal(t, &valkeysegment.LookupError{
				Class: valkeysegment.ClassProtocol, Failed: tt.wantFailed, Total: len(tt.lookups),
			}, err)
			assert.Equal(t, tt.wantStates, states(tt.lookups))
		})
	}
}

func TestAKeyThatAppearsBetweenTheCheckAndTheLookupIsNeverANonMember(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		replies    []string
		wantStates []segment.State
	}{
		{
			name:       "created after the key check, the member is a member",
			replies:    []string{":0\r\n", "*1\r\n:1\r\n"},
			wantStates: []segment.State{segment.Member},
		},
		{
			name:       "created after the key check, a non-member stays unknown",
			replies:    []string{":0\r\n", "*1\r\n:0\r\n"},
			wantStates: []segment.State{segment.Unknown},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			prov := newProvider(t, srv, baseOptions(nil))

			require.NoError(t, prov.Init(context.Background()))
			srv.answerSequence(tt.replies...)

			lookups := []segment.Lookup{lk(segBeta, "zed")}

			require.NoError(t, prov.Contains(context.Background(), lookups))
			assert.Equal(t, tt.wantStates, states(lookups))
		})
	}
}

func TestExistsTrustsNothingOfABatchWithAWrongShape(t *testing.T) {
	t.Parallel()

	srv := loaded(t)
	prov := newProvider(t, srv, baseOptions(nil))

	require.NoError(t, prov.Init(context.Background()))
	srv.answerSequence(replyOne, ":7\r\n")

	got, err := prov.Exists(context.Background(), []string{segBeta, segGhost})

	assert.Equal(t, &valkeysegment.LookupError{Class: valkeysegment.ClassProtocol, Failed: 2, Total: 2}, err)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, got)
}
