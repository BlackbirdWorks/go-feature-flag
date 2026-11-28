package redissegment_test

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

const (
	cmdSMIGhostAlice   = "smismember semaphore:segment:{ghost} alice"
	cmdExistsFlagAlice = "exists flag:alice"
	keyFlagAlice       = "flag:alice"
	segGone            = "gone"
	nameKeysLayout     = "keys layout"
	carolID            = "carol"
	segChurned         = "churned"
	segBeta            = "beta"
	segGhost           = "ghost"
	idAlice            = "alice"
	cmdSMIsMember      = "smismember"
	cmdExists          = "exists"
	cmdEval            = "eval"
	cmdEvalRO          = "eval_ro"
	cmdEvalSHA         = "evalsha"
	msgWrongType       = "WRONGTYPE"
	flagPrefix         = "flag:"
	msgProtocol1       = "redis segment: protocol: 1 of 1 lookups failed"
	keyBeta            = "semaphore:segment:{beta}"
	keyChurned         = "semaphore:segment:{churned}"
	cmdBeta            = "smismember " + keyBeta
	existsBeta         = "exists " + keyBeta
)

var (
	errDialFailed = errors.New("dial failed")
	// errDial is a transport failure: a [net.Error], as the failure of a connection is.
	errDial  = &net.OpError{Op: "dial", Net: "tcp", Err: errDialFailed}
	errNoRun = errors.New("must not be called")
)

// serverError is a reply error of the store (the interface that go-redis uses to tell them from network errors).
type serverError struct{ msg string }

func (e serverError) Error() string { return e.msg }
func (serverError) RedisError()     {}

// timeoutError is a network error that is a timeout.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return false }

// errParse is the error of go-redis for a reply that is not an integer.
func errParse() error {
	return &strconv.NumError{Func: "ParseInt", Num: "invalid", Err: strconv.ErrSyntax}
}

func lk(seg, id string) segment.Lookup { return segment.Lookup{Segment: seg, ID: id} }

func states(lookups []segment.Lookup) []segment.State {
	out := make([]segment.State, len(lookups))
	for i := range lookups {
		out[i] = lookups[i].State
	}

	return out
}

func twoSegments() map[string]redissegment.SegmentOptions {
	return map[string]redissegment.SegmentOptions{segBeta: {}, segChurned: {}}
}

func newProvider(t *testing.T, store *fakeStore, opts *redissegment.Options) *redissegment.Provider {
	t.Helper()

	prov, err := redissegment.New(store, opts)
	require.NoError(t, err)

	return prov
}

func TestContainsAnswers(t *testing.T) {
	t.Parallel()

	data := func() *fakeStore {
		return &fakeStore{
			sets: map[string]map[string]bool{
				keyBeta:    {idAlice: true, carolID: true},
				keyChurned: {carolID: true},
			},
			keys: map[string]bool{keyFlagAlice: true},
		}
	}

	tests := []struct {
		opts         *redissegment.Options
		name         string
		lookups      []segment.Lookup
		wantStates   []segment.State
		wantCommands [][]string
	}{
		{
			name:       "member and not member of one set in one command",
			opts:       &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates: []segment.State{segment.Member, segment.NotMember},
			wantCommands: [][]string{
				{cmdBeta + " alice zed", existsBeta},
			},
		},
		{
			name: "two segments interleaved keep their positions",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			lookups: []segment.Lookup{
				lk(segBeta, idAlice), lk(segChurned, idAlice), lk(segBeta, "zed"), lk(segChurned, carolID),
			},
			wantStates: []segment.State{segment.Member, segment.NotMember, segment.NotMember, segment.Member},
			wantCommands: [][]string{
				{
					cmdBeta + " alice zed",
					existsBeta,
					"smismember " + keyChurned + " alice carol",
					"exists " + keyChurned,
				},
			},
		},
		{
			name: "missing key is unknown by default",
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingUnknown,
				Segments:   map[string]redissegment.SegmentOptions{segGhost: {}},
			},
			lookups:    []segment.Lookup{lk(segGhost, idAlice)},
			wantStates: []segment.State{segment.Unknown},
			wantCommands: [][]string{
				{cmdSMIGhostAlice, "exists semaphore:segment:{ghost}"},
			},
		},
		{
			name: "missing key is not member on request, without the exists command",
			opts: &redissegment.Options{
				Segments:   map[string]redissegment.SegmentOptions{segGhost: {}},
				MissingKey: redissegment.MissingNotMember,
			},
			lookups:    []segment.Lookup{lk(segGhost, idAlice)},
			wantStates: []segment.State{segment.NotMember},
			wantCommands: [][]string{
				{cmdSMIGhostAlice},
			},
		},
		{
			name: "custom key and prefix",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				KeyPrefix: "prov:",
				Segments: map[string]redissegment.SegmentOptions{
					"a": {}, "b": {Key: keyBeta},
				},
			},
			lookups:    []segment.Lookup{lk("a", "x"), lk("b", idAlice)},
			wantStates: []segment.State{segment.Unknown, segment.Member},
			wantCommands: [][]string{
				{"smismember prov:{a} x", "exists prov:{a}", cmdBeta + " alice", existsBeta},
			},
		},
		{
			name: "keys layout is one exists per id",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				Segments: map[string]redissegment.SegmentOptions{
					"f": {Layout: redissegment.LayoutKeys, Key: flagPrefix},
				},
			},
			lookups:    []segment.Lookup{lk("f", idAlice), lk("f", "zed")},
			wantStates: []segment.State{segment.Member, segment.NotMember},
			wantCommands: [][]string{
				{cmdExistsFlagAlice, "exists flag:zed"},
			},
		},
		{
			name: "keys layout default prefix, empty id is not asked",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				Segments: map[string]redissegment.SegmentOptions{"f": {Layout: redissegment.LayoutKeys}},
			},
			lookups:    []segment.Lookup{lk("f", ""), lk("f", "x")},
			wantStates: []segment.State{segment.Unknown, segment.NotMember},
			wantCommands: [][]string{
				{"exists semaphore:segment:f:x"},
			},
		},
		{
			name: "set and keys segments share one pipeline",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				Segments: map[string]redissegment.SegmentOptions{
					segBeta: {}, "f": {Layout: redissegment.LayoutKeys, Key: flagPrefix},
				},
			},
			lookups:    []segment.Lookup{lk("f", idAlice), lk(segBeta, idAlice)},
			wantStates: []segment.State{segment.Member, segment.Member},
			wantCommands: [][]string{
				{cmdBeta + " alice", existsBeta, cmdExistsFlagAlice},
			},
		},
		{
			name:         "segment that is not configured stays unknown and sends nothing",
			opts:         &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			lookups:      []segment.Lookup{lk("other", idAlice)},
			wantStates:   []segment.State{segment.Unknown},
			wantCommands: [][]string{},
		},
		{
			name:         "no lookups sends nothing",
			opts:         &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			lookups:      []segment.Lookup{},
			wantStates:   []segment.State{},
			wantCommands: [][]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := data()
			prov := newProvider(t, store, tt.opts)

			err := prov.Contains(context.Background(), tt.lookups)

			require.NoError(t, err)
			assert.Equal(t, tt.wantStates, states(tt.lookups))
			assert.Equal(t, tt.wantCommands, store.commands())
		})
	}
}

func TestContainsDoesNotChangeSegmentOrID(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)
	lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segChurned, "bob")}

	require.NoError(t, prov.Contains(context.Background(), lookups))

	assert.Equal(t, []segment.Lookup{
		{Segment: segBeta, ID: idAlice, State: segment.Member},
		{Segment: segChurned, ID: "bob", State: segment.Unknown},
	}, lookups)
}

type failureCase struct {
	wantSentry  error
	store       *fakeStore
	ctx         func() (context.Context, context.CancelFunc)
	name        string
	wantMessage string
	lookups     []segment.Lookup
	wantStates  []segment.State
	timeout     time.Duration
	wantFailed  int
	wantPipes   int
	wantClass   redissegment.Class
}

func runFailureCases(t *testing.T, tests []failureCase) {
	t.Helper()

	for idx := range tests {
		tt := &tests[idx]

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.Background(), context.CancelFunc(func() {})
			if tt.ctx != nil {
				ctx, cancel = tt.ctx()
			}

			defer cancel()

			prov := newProvider(t, tt.store, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				Timeout: tt.timeout,
				Segments: map[string]redissegment.SegmentOptions{
					segBeta: {}, segChurned: {}, "f": {Layout: redissegment.LayoutKeys, Key: flagPrefix},
				},
			})

			err := prov.Contains(ctx, tt.lookups)

			var lookupErr *redissegment.LookupError

			require.ErrorAs(t, err, &lookupErr)
			assert.Equal(t, tt.wantClass, lookupErr.Class)
			assert.Equal(t, tt.wantFailed, lookupErr.Failed)
			assert.Len(t, tt.lookups, lookupErr.Total)
			require.ErrorIs(t, err, tt.wantSentry)
			assert.Equal(t, tt.wantMessage, err.Error())
			assert.Equal(t, tt.wantStates, states(tt.lookups))
			assert.Len(t, tt.store.commands(), tt.wantPipes)
		})
	}
}

func failureSetBeta() map[string]map[string]bool {
	return map[string]map[string]bool{keyBeta: {idAlice: true}, keyChurned: {carolID: true}}
}

func TestContainsFailures(t *testing.T) {
	t.Parallel()

	setBeta := failureSetBeta()

	runFailureCases(t, []failureCase{
		{
			name:        "connection error leaves everything unknown",
			store:       &fakeStore{sets: setBeta, exec: func(context.Context) error { return errDial }},
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segChurned, carolID)},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: msgUnavailable2of2,
		},
		{
			name: "server error on every command",
			store: &fakeStore{
				sets: setBeta,
				reply: func([]any) *fakeReply {
					return &fakeReply{err: serverError{msg: "NOPERM this user has no permissions"}}
				},
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassServer,
			wantSentry:  redissegment.ErrServer,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: "redis segment: server: 1 of 1 lookups failed",
		},
		{
			name: "one segment fails, the other keeps its answers",
			store: &fakeStore{
				sets: setBeta,
				reply: func(args []any) *fakeReply {
					if args[1] == keyChurned {
						return &fakeReply{err: serverError{msg: msgWrongType}}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segChurned, carolID), lk(segBeta, "zed")},
			wantStates:  []segment.State{segment.Member, segment.Unknown, segment.NotMember},
			wantClass:   redissegment.ClassServer,
			wantSentry:  redissegment.ErrServer,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: "redis segment: server: 1 of 3 lookups failed",
		},
		{
			name: "the key check fails: members are certain, the others unknown",
			store: &fakeStore{
				sets: setBeta,
				reply: func(args []any) *fakeReply {
					if args[0] == cmdExists {
						return &fakeReply{err: errDial}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
		{
			name: "key check with a wrong reply type",
			store: &fakeStore{
				sets: setBeta,
				reply: func(args []any) *fakeReply {
					if args[0] == cmdExists {
						return &fakeReply{value: "yes"}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk(segBeta, "zed")},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassProtocol,
			wantSentry:  redissegment.ErrProtocol,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgProtocol1,
		},
	})
}

func TestContainsReplyFailures(t *testing.T) {
	t.Parallel()

	setBeta := failureSetBeta()

	runFailureCases(t, []failureCase{
		{
			name: "wrong reply type",
			store: &fakeStore{
				sets:  setBeta,
				reply: func([]any) *fakeReply { return &fakeReply{value: "OK"} },
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassProtocol,
			wantSentry:  redissegment.ErrProtocol,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgProtocol1,
		},
		{
			name: "reply with the wrong number of answers",
			store: &fakeStore{
				sets: setBeta,
				reply: func(args []any) *fakeReply {
					if args[0] == cmdSMIsMember {
						return &fakeReply{value: []any{int64(1)}}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates:  []segment.State{segment.Unknown, segment.Unknown},
			wantClass:   redissegment.ClassProtocol,
			wantSentry:  redissegment.ErrProtocol,
			wantFailed:  2,
			wantPipes:   1,
			wantMessage: "redis segment: protocol: 2 of 2 lookups failed",
		},
		{
			name: "reply with an answer that is not 0 or 1",
			store: &fakeStore{
				sets: setBeta,
				reply: func(args []any) *fakeReply {
					if args[0] == cmdSMIsMember {
						return &fakeReply{value: []any{int64(7)}}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassProtocol,
			wantSentry:  redissegment.ErrProtocol,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgProtocol1,
		},
		{
			name: "nil reply",
			store: &fakeStore{
				sets:  setBeta,
				reply: func([]any) *fakeReply { return &fakeReply{} },
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassProtocol,
			wantSentry:  redissegment.ErrProtocol,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgProtocol1,
		},
		{
			name: "keys layout reply that is not an integer",
			store: &fakeStore{
				reply: func([]any) *fakeReply { return &fakeReply{value: "x"} },
			},
			lookups:     []segment.Lookup{lk("f", idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassProtocol,
			wantSentry:  redissegment.ErrProtocol,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgProtocol1,
		},
		{
			name: "keys layout error on one id",
			store: &fakeStore{
				keys: map[string]bool{keyFlagAlice: true},
				reply: func(args []any) *fakeReply {
					if args[1] == "flag:zed" {
						return &fakeReply{err: errDial}
					}

					return nil
				},
			},
			lookups:     []segment.Lookup{lk("f", idAlice), lk("f", "zed")},
			wantStates:  []segment.State{segment.Member, segment.Unknown},
			wantClass:   redissegment.ClassUnavailable,
			wantSentry:  redissegment.ErrUnavailable,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: msgUnavailable1of2,
		},
		{
			name: "the per-call timeout passes",
			store: &fakeStore{sets: setBeta, exec: func(ctx context.Context) error {
				<-ctx.Done()

				return ctx.Err()
			}},
			timeout:     time.Nanosecond,
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassTimeout,
			wantSentry:  redissegment.ErrTimeout,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: "redis segment: timeout: 1 of 1 lookups failed",
		},
		{
			name:        "a network timeout is a timeout",
			store:       &fakeStore{sets: setBeta, exec: func(context.Context) error { return timeoutError{} }},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassTimeout,
			wantSentry:  redissegment.ErrTimeout,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: "redis segment: timeout: 1 of 1 lookups failed",
		},
		{
			name:  "the context ends before the call: nothing is sent",
			store: &fakeStore{sets: setBeta, exec: func(context.Context) error { return errNoRun }},
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				return ctx, cancel
			},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassCanceled,
			wantSentry:  redissegment.ErrCanceled,
			wantFailed:  1,
			wantPipes:   0,
			wantMessage: "redis segment: canceled: 1 of 1 lookups failed",
		},
		{
			name:        "a context error that is not from the caller is a cancel",
			store:       &fakeStore{sets: setBeta, exec: func(context.Context) error { return context.Canceled }},
			lookups:     []segment.Lookup{lk(segBeta, idAlice)},
			wantStates:  []segment.State{segment.Unknown},
			wantClass:   redissegment.ClassCanceled,
			wantSentry:  redissegment.ErrCanceled,
			wantFailed:  1,
			wantPipes:   1,
			wantMessage: "redis segment: canceled: 1 of 1 lookups failed",
		},
	})
}

func TestContainsCancelMidCall(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := &fakeStore{
		sets: map[string]map[string]bool{keyBeta: {idAlice: true}},
		exec: func(context.Context) error {
			cancel()

			return context.Canceled
		},
	}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)
	lookups := []segment.Lookup{lk(segBeta, idAlice)}

	err := prov.Contains(ctx, lookups)

	require.ErrorIs(t, err, redissegment.ErrCanceled)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
}

func TestErrorMessageHasNoIDsNorKeys(t *testing.T) {
	t.Parallel()

	store := &fakeStore{exec: func(context.Context) error { return serverError{msg: "secret-key alice"} }}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)

	err := prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)})

	require.Error(t, err)
	assert.Equal(t, "redis segment: server: 1 of 1 lookups failed", err.Error())
	assert.NotContains(t, err.Error(), idAlice)
	assert.NotContains(t, err.Error(), "secret-key")
}

func TestContainsObserver(t *testing.T) {
	t.Parallel()

	setBeta := map[string]map[string]bool{keyBeta: {idAlice: true}}

	tests := []struct {
		store         *fakeStore
		wantStatsFail map[string]uint64
		name          string
		lookups       []segment.Lookup
		wantObserved  redissegment.Observation
		wantFailure   redissegment.Class
	}{
		{
			name:    "answered",
			store:   &fakeStore{sets: setBeta},
			lookups: []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantObserved: redissegment.Observation{
				Lookups: 2, Answered: 2, Unknown: 0, RoundTrips: 1,
			},
			wantStatsFail: map[string]uint64{},
		},
		{
			name:    "failed",
			store:   &fakeStore{exec: func(context.Context) error { return errDial }},
			lookups: []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantObserved: redissegment.Observation{
				Lookups: 2, Unknown: 2, RoundTrips: 1, Failure: redissegment.ClassUnavailable,
			},
			wantStatsFail: map[string]uint64{classUnavailable: 1},
		},
		{
			name:    "nothing to ask",
			store:   &fakeStore{},
			lookups: []segment.Lookup{lk("other", "x")},
			wantObserved: redissegment.Observation{
				Lookups: 1, Unknown: 1,
			},
			wantStatsFail: map[string]uint64{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{}
			counters := &redissegment.Counters{}
			prov := newProvider(t, tt.store, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				Segments: twoSegments(),
				Observer: multi{rec, counters},
			})

			_ = prov.Contains(context.Background(), tt.lookups)

			got := rec.all()
			require.Len(t, got, 1)
			assert.GreaterOrEqual(t, got[0].Duration, callStep)

			got[0].Duration = 0
			assert.Equal(t, tt.wantObserved, got[0])

			stats := counters.Snapshot()
			assert.Equal(t, uint64(1), stats.Calls)
			assert.Equal(t, uint64(tt.wantObserved.Lookups), stats.Lookups)
			assert.Equal(t, uint64(tt.wantObserved.Answered), stats.Answered)
			assert.Equal(t, uint64(tt.wantObserved.Unknown), stats.Unknown)
			assert.Equal(t, uint64(tt.wantObserved.RoundTrips), stats.RoundTrips)
			assert.GreaterOrEqual(t, stats.Duration, callStep)
			assert.Equal(t, tt.wantStatsFail, stats.Failures)
		})
	}
}

func TestNoLookupsIsNotObserved(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	prov := newProvider(t, &fakeStore{}, &redissegment.Options{MissingKey: redissegment.MissingUnknown, Observer: rec})

	require.NoError(t, prov.Contains(context.Background(), nil))
	assert.Empty(t, rec.all())
}

type recorder struct {
	obs []redissegment.Observation
	mu  sync.Mutex
}

func (r *recorder) Observe(o redissegment.Observation) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.obs = append(r.obs, o)
}

func (r *recorder) all() []redissegment.Observation {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]redissegment.Observation{}, r.obs...)
}

type multi []redissegment.Observer

func (m multi) Observe(o redissegment.Observation) {
	for _, each := range m {
		each.Observe(o)
	}
}

func TestContainsConcurrent(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)

	const workers = 16

	var wg sync.WaitGroup

	got := make([][]segment.State, workers)
	errs := make([]error, workers)

	for worker := range workers {
		wg.Go(func() {
			lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed"), lk(segChurned, idAlice)}
			errs[worker] = prov.Contains(context.Background(), lookups)
			got[worker] = states(lookups)
		})
	}

	wg.Wait()

	want := []segment.State{segment.Member, segment.NotMember, segment.Unknown}

	for worker := range workers {
		require.NoError(t, errs[worker])
		assert.Equal(t, want, got[worker])
	}

	assert.Len(t, store.commands(), workers)
}

func TestOptionsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		client  redissegment.Client
		wantErr error
		opts    *redissegment.Options
		name    string
	}{
		{
			name:   "valid",
			client: &fakeStore{},
			opts:   &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
		},
		{name: "nil client", wantErr: redissegment.ErrNilClient},
		{
			name:    "negative timeout",
			client:  &fakeStore{},
			opts:    &redissegment.Options{MissingKey: redissegment.MissingUnknown, Timeout: -1},
			wantErr: redissegment.ErrInvalidOptions,
		},
		{
			name: "bad missing key", client: &fakeStore{}, opts: &redissegment.Options{MissingKey: "x"},
			wantErr: redissegment.ErrInvalidOptions,
		},
		{
			name:   "bad segment name",
			client: &fakeStore{},
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingUnknown,
				Segments:   map[string]redissegment.SegmentOptions{"Bad Name": {}},
			},
			wantErr: redissegment.ErrInvalidOptions,
		},
		{
			name:   "bad layout",
			client: &fakeStore{},
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingUnknown,
				Segments:   map[string]redissegment.SegmentOptions{"a": {Layout: "x"}},
			},
			wantErr: redissegment.ErrInvalidOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov, err := redissegment.New(tt.client, tt.opts)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, prov)

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, prov)
			assert.NoError(t, prov.Close())
		})
	}
}

func TestDial(t *testing.T) {
	t.Parallel()

	_, err := redissegment.Dial(nil, &redissegment.Options{MissingKey: redissegment.MissingUnknown})
	require.ErrorIs(t, err, redissegment.ErrNilClient)

	_, err = redissegment.Dial(
		&redis.UniversalOptions{Addrs: []string{unreachableAddr}},
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Timeout: -1},
	)
	require.ErrorIs(t, err, redissegment.ErrInvalidOptions)

	prov, err := redissegment.Dial(
		&redis.UniversalOptions{Addrs: []string{unreachableAddr}},
		&redissegment.Options{MissingKey: redissegment.MissingUnknown},
	)
	require.NoError(t, err)
	require.NoError(t, prov.Close())
}

func TestClassAndLookupError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantSentry error
		wantString string
		class      redissegment.Class
	}{
		{class: redissegment.ClassNone, wantString: "none", wantSentry: nil},
		{class: redissegment.ClassCanceled, wantString: "canceled", wantSentry: redissegment.ErrCanceled},
		{class: redissegment.ClassTimeout, wantString: "timeout", wantSentry: redissegment.ErrTimeout},
		{class: redissegment.ClassUnavailable, wantString: classUnavailable, wantSentry: redissegment.ErrUnavailable},
		{class: redissegment.ClassServer, wantString: "server", wantSentry: redissegment.ErrServer},
		{class: redissegment.ClassProtocol, wantString: "protocol", wantSentry: redissegment.ErrProtocol},
		{class: redissegment.Class(99), wantString: "invalid", wantSentry: nil},
	}

	for _, tt := range tests {
		t.Run(tt.wantString, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantString, tt.class.String())
			assert.Equal(t, tt.wantSentry, (&redissegment.LookupError{Class: tt.class}).Unwrap())
		})
	}

	assert.Equal(t, []redissegment.Class{
		redissegment.ClassCanceled, redissegment.ClassTimeout, redissegment.ClassUnavailable,
		redissegment.ClassServer, redissegment.ClassProtocol,
	}, redissegment.Classes())
}

func TestCountersIgnoreInvalidValues(t *testing.T) {
	t.Parallel()

	c := &redissegment.Counters{}
	c.Observe(
		redissegment.Observation{Lookups: -1, Answered: -1, Unknown: -1, RoundTrips: -1, Duration: -1, Failure: 99},
	)

	assert.Equal(t, redissegment.Stats{Calls: 1, Failures: map[string]uint64{}}, c.Snapshot())
}
