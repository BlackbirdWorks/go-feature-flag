package redissegment_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

func TestAtomicUsesOneScriptForEachSet(t *testing.T) {
	t.Parallel()

	script := "eval " + redissegment.Script + " 1 "
	bySHA := "evalsha " + redissegment.ScriptSHA + " 1 "

	tests := []struct {
		opts         *redissegment.Options
		name         string
		lookups      []segment.Lookup
		wantCommands [][]string
		wantStates   []segment.State
	}{
		{
			name: "atomic is one script that checks the key and the ids (loaded by the first call)",
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingUnknown,
				Atomic:     true,
				Segments:   twoSegments(),
			},
			lookups:      []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantCommands: [][]string{{bySHA + keyBeta + " alice zed"}, {script + keyBeta + " alice zed"}},
			wantStates:   []segment.State{segment.Member, segment.NotMember},
		},
		{
			name: "a script for each set in the same pipeline",
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingUnknown,
				Atomic:     true,
				Segments:   twoSegments(),
			},
			lookups: []segment.Lookup{lk(segBeta, idAlice), lk(segChurned, idAlice), lk(segBeta, "zed")},
			wantCommands: [][]string{
				{bySHA + keyBeta + " alice zed", bySHA + keyChurned + " alice"},
				{script + keyBeta + " alice zed", script + keyChurned + " alice"},
			},
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.NotMember},
		},
		{
			name:         "without atomic it is two commands",
			opts:         &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			lookups:      []segment.Lookup{lk(segBeta, idAlice)},
			wantCommands: [][]string{{cmdBeta + " alice", existsBeta}},
			wantStates:   []segment.State{segment.Member},
		},
		{
			name: "atomic without a key check has nothing to protect",
			opts: &redissegment.Options{
				Atomic: true, MissingKey: redissegment.MissingNotMember, Segments: twoSegments(),
			},
			lookups:      []segment.Lookup{lk(segBeta, idAlice)},
			wantCommands: [][]string{{cmdBeta + " alice"}},
			wantStates:   []segment.State{segment.Member},
		},
		{
			name: "atomic with only a keys segment has nothing to protect",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown,
				Atomic: true,
				Segments: map[string]redissegment.SegmentOptions{
					"f": {Layout: redissegment.LayoutKeys, Key: flagPrefix},
				},
			},
			lookups:      []segment.Lookup{lk("f", idAlice)},
			wantCommands: [][]string{{cmdExistsFlagAlice}},
			wantStates:   []segment.State{segment.Member},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				sets: map[string]map[string]bool{keyBeta: {idAlice: true}},
				keys: map[string]bool{keyFlagAlice: true},
			}
			prov := newProvider(t, store, tt.opts)

			require.NoError(t, prov.Contains(context.Background(), tt.lookups))
			assert.Equal(t, tt.wantStates, states(tt.lookups))
			assert.Equal(t, tt.wantCommands, store.commands())
		})
	}
}

func TestAtomicScriptReplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reply      any
		wantErr    error
		name       string
		wantStates []segment.State
	}{
		{
			name:       "key missing",
			reply:      []any{int64(0), int64(0), int64(0)},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
		},
		{
			name:       "key there",
			reply:      []any{int64(1), int64(1), int64(0)},
			wantStates: []segment.State{segment.Member, segment.NotMember},
		},
		{
			name:       "a member of a key that the check says is missing is still a member",
			reply:      []any{int64(0), int64(1), int64(0)},
			wantStates: []segment.State{segment.Member, segment.Unknown},
		},
		{
			name:       "too few replies",
			reply:      []any{int64(1), int64(1)},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
			wantErr:    redissegment.ErrProtocol,
		},
		{
			name:       "a reply that is not 0 or 1",
			reply:      []any{int64(1), int64(5), int64(0)},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
			wantErr:    redissegment.ErrProtocol,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{reply: func([]any) *fakeReply { return &fakeReply{value: tt.reply} }}
			prov := newProvider(
				t,
				store,
				&redissegment.Options{MissingKey: redissegment.MissingUnknown, Atomic: true, Segments: twoSegments()},
			)
			lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")}

			err := prov.Contains(context.Background(), lookups)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantStates, states(lookups))
		})
	}
}

func TestInitStatusAndShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		store      *fakeStore
		name       string
		wantStatus redissegment.Status
	}{
		{
			name:       "the store answers",
			store:      &fakeStore{},
			wantStatus: redissegment.Status{Ready: true},
		},
		{
			name:       nameStoreDown,
			store:      &fakeStore{exec: func(context.Context) error { return errDial }},
			wantErr:    redissegment.ErrUnavailable,
			wantStatus: redissegment.Status{LastFailure: redissegment.ClassUnavailable},
		},
		{
			name: "the reply is not a pong",
			store: &fakeStore{reply: func([]any) *fakeReply {
				return &fakeReply{}
			}},
			wantErr:    redissegment.ErrProtocol,
			wantStatus: redissegment.Status{LastFailure: redissegment.ClassProtocol},
		},
		{
			name: "the reply is another string than PONG",
			store: &fakeStore{reply: func([]any) *fakeReply {
				return &fakeReply{value: "OK"}
			}},
			wantErr:    redissegment.ErrProtocol,
			wantStatus: redissegment.Status{LastFailure: redissegment.ClassProtocol},
		},
		{
			name: "the reply is an error of the store",
			store: &fakeStore{reply: func([]any) *fakeReply {
				return &fakeReply{err: serverError{msg: "NOPERM"}}
			}},
			wantErr:    redissegment.ErrServer,
			wantStatus: redissegment.Status{LastFailure: redissegment.ClassServer},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(t, tt.store, &redissegment.Options{MissingKey: redissegment.MissingUnknown})

			err := prov.Init(context.Background())

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorIs(t, err, redissegment.ErrInit)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantStatus, prov.Status())

			require.NoError(t, prov.Shutdown(boundedCtx(t)))

			closed := tt.wantStatus
			closed.Closed = true
			assert.Equal(t, closed, prov.Status())
		})
	}
}

func TestStatusFollowsTheCalls(t *testing.T) {
	t.Parallel()

	down := false
	store := &fakeStore{
		sets: map[string]map[string]bool{keyBeta: {idAlice: true}},
		exec: func(context.Context) error {
			if down {
				return errDial
			}

			return nil
		},
	}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
	)

	require.NoError(t, prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, redissegment.Status{Ready: true}, prov.Status())

	down = true

	require.Error(t, prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, redissegment.Status{Ready: true, LastFailure: redissegment.ClassUnavailable}, prov.Status())
}

func TestExists(t *testing.T) {
	t.Parallel()

	segs := map[string]redissegment.SegmentOptions{
		segBeta: {}, segGone: {}, "f": {Layout: redissegment.LayoutKeys, Key: flagPrefix},
	}

	tests := []struct {
		name       string
		store      *fakeStore
		names      []string
		wantStates []segment.State
		wantErr    error
		wantCmds   [][]string
	}{
		{
			name:       "loaded and not loaded sets in one pipeline",
			store:      &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}},
			names:      []string{segBeta, segGone},
			wantStates: []segment.State{segment.Member, segment.NotMember},
			wantCmds:   [][]string{{existsBeta, "exists semaphore:segment:{gone}"}},
		},
		{
			name:       "keys layout and unconfigured segments cannot tell",
			store:      &fakeStore{},
			names:      []string{"f", segOther},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
			wantCmds:   [][]string{},
		},
		{
			name:       "no names",
			store:      &fakeStore{},
			names:      []string{},
			wantStates: []segment.State{},
			wantCmds:   [][]string{},
		},
		{
			name:       nameStoreDown,
			store:      &fakeStore{exec: func(context.Context) error { return errDial }},
			names:      []string{segBeta},
			wantStates: []segment.State{segment.Unknown},
			wantErr:    redissegment.ErrUnavailable,
			wantCmds:   [][]string{{existsBeta}},
		},
		{
			name: "a reply that is not an integer",
			store: &fakeStore{reply: func([]any) *fakeReply {
				return &fakeReply{value: "x"}
			}},
			names:      []string{segBeta},
			wantStates: []segment.State{segment.Unknown},
			wantErr:    redissegment.ErrProtocol,
			wantCmds:   [][]string{{existsBeta}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(
				t,
				tt.store,
				&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: segs},
			)

			got, err := prov.Exists(context.Background(), tt.names)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantStates, got)
			assert.Equal(t, tt.wantCmds, tt.store.commands())
		})
	}
}

func TestDocumentedDefaults(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 100*time.Millisecond, redissegment.DefaultTimeout)
	assert.Equal(t, "semaphore:segment:", redissegment.DefaultKeyPrefix)

	tests := []struct {
		name        string
		timeout     time.Duration
		wantTimeout time.Duration
	}{
		{name: "the default timeout", wantTimeout: redissegment.DefaultTimeout},
		{name: "a configured timeout", timeout: 3 * time.Second, wantTimeout: 3 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// inside the bubble the clock is fake and starts at a fixed time, so the deadline is exact
			synctest.Test(t, func(t *testing.T) {
				var deadline time.Time

				start := time.Now()
				store := &fakeStore{exec: func(ctx context.Context) error {
					deadline, _ = ctx.Deadline()

					return nil
				}}
				prov := newProvider(t, store, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
					Timeout:  tt.timeout,
					Segments: map[string]redissegment.SegmentOptions{"ghost": {}},
				})
				lookups := []segment.Lookup{lk("ghost", idAlice)}

				require.NoError(t, prov.Contains(context.Background(), lookups))

				assert.Equal(t, start.Add(tt.wantTimeout), deadline)
				assert.Equal(t, []segment.State{segment.Unknown}, states(lookups), "a missing key is unknown")
				assert.Equal(
					t,
					[][]string{{cmdSMIGhostAlice, "exists semaphore:segment:{ghost}"}},
					store.commands(),
				)
			})
		})
	}
}
