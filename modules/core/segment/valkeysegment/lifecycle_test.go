package valkeysegment_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

func TestAtomicUsesOneScriptForEachSet(t *testing.T) {
	t.Parallel()

	script := "eval " + valkeysegment.Script + " 1 "

	tests := []struct {
		name         string
		lookups      []segment.Lookup
		wantCommands [][]string
		wantStates   []segment.State
		opts         valkeysegment.Options
	}{
		{
			name:         "atomic is one script that checks the key and the ids",
			opts:         valkeysegment.Options{Atomic: true, Segments: twoSegments()},
			lookups:      []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantCommands: [][]string{{script + keyBeta + " alice zed"}},
			wantStates:   []segment.State{segment.Member, segment.NotMember},
		},
		{
			name:         "a script for each set in the same pipeline",
			opts:         valkeysegment.Options{Atomic: true, Segments: twoSegments()},
			lookups:      []segment.Lookup{lk(segBeta, idAlice), lk(segChurned, idAlice), lk(segBeta, "zed")},
			wantCommands: [][]string{{script + keyBeta + " alice zed", script + keyChurned + " alice"}},
			wantStates:   []segment.State{segment.Member, segment.Unknown, segment.NotMember},
		},
		{
			name:         "without atomic it is two commands",
			opts:         valkeysegment.Options{Segments: twoSegments()},
			lookups:      []segment.Lookup{lk(segBeta, idAlice)},
			wantCommands: [][]string{{cmdBeta + " alice", existsBeta}},
			wantStates:   []segment.State{segment.Member},
		},
		{
			name: "atomic without a key check has nothing to protect",
			opts: valkeysegment.Options{
				Atomic: true, MissingKey: valkeysegment.MissingNotMember, Segments: twoSegments(),
			},
			lookups:      []segment.Lookup{lk(segBeta, idAlice)},
			wantCommands: [][]string{{cmdBeta + " alice"}},
			wantStates:   []segment.State{segment.Member},
		},
		{
			name: "atomic with only a keys segment has nothing to protect",
			opts: valkeysegment.Options{
				Atomic: true,
				Segments: map[string]valkeysegment.SegmentOptions{
					"f": {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
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
			wantErr:    valkeysegment.ErrProtocol,
		},
		{
			name:       "a reply that is not 0 or 1",
			reply:      []any{int64(1), int64(5), int64(0)},
			wantStates: []segment.State{segment.Unknown, segment.Unknown},
			wantErr:    valkeysegment.ErrProtocol,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{reply: func([]any) *fakeReply { return &fakeReply{value: tt.reply} }}
			prov := newProvider(t, store, valkeysegment.Options{Atomic: true, Segments: twoSegments()})
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
		wantStatus valkeysegment.Status
	}{
		{
			name:       "the store answers",
			store:      &fakeStore{},
			wantStatus: valkeysegment.Status{Ready: true},
		},
		{
			name:       "the store is down",
			store:      &fakeStore{exec: func(context.Context) error { return errDial }},
			wantErr:    valkeysegment.ErrUnavailable,
			wantStatus: valkeysegment.Status{LastFailure: valkeysegment.ClassUnavailable},
		},
		{
			name: "the reply is not a pong",
			store: &fakeStore{reply: func([]any) *fakeReply {
				return &fakeReply{}
			}},
			wantErr:    valkeysegment.ErrProtocol,
			wantStatus: valkeysegment.Status{LastFailure: valkeysegment.ClassProtocol},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(t, tt.store, valkeysegment.Options{})

			err := prov.Init(context.Background())

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorIs(t, err, valkeysegment.ErrInit)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantStatus, prov.Status())

			require.NoError(t, prov.Shutdown(context.Background()))

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
	prov := newProvider(t, store, valkeysegment.Options{Segments: twoSegments()})

	require.NoError(t, prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, valkeysegment.Status{Ready: true}, prov.Status())

	down = true

	require.Error(t, prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, valkeysegment.Status{Ready: true, LastFailure: valkeysegment.ClassUnavailable}, prov.Status())
}

func TestExists(t *testing.T) {
	t.Parallel()

	segs := map[string]valkeysegment.SegmentOptions{
		segBeta: {}, segGone: {}, "f": {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
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
			names:      []string{"f", "other"},
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
			name:       "the store is down",
			store:      &fakeStore{exec: func(context.Context) error { return errDial }},
			names:      []string{segBeta},
			wantStates: []segment.State{segment.Unknown},
			wantErr:    valkeysegment.ErrUnavailable,
			wantCmds:   [][]string{{existsBeta}},
		},
		{
			name: "a reply that is not an integer",
			store: &fakeStore{reply: func([]any) *fakeReply {
				return &fakeReply{value: "x"}
			}},
			names:      []string{segBeta},
			wantStates: []segment.State{segment.Unknown},
			wantErr:    valkeysegment.ErrProtocol,
			wantCmds:   [][]string{{existsBeta}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(t, tt.store, valkeysegment.Options{Segments: segs})

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

	assert.Equal(t, 100*time.Millisecond, valkeysegment.DefaultTimeout)
	assert.Equal(t, "semaphore:segment:", valkeysegment.DefaultKeyPrefix)

	var deadline time.Time

	store := &fakeStore{exec: func(ctx context.Context) error {
		deadline, _ = ctx.Deadline()

		return nil
	}}
	prov := newProvider(t, store, valkeysegment.Options{Segments: map[string]valkeysegment.SegmentOptions{"ghost": {}}})
	lookups := []segment.Lookup{lk("ghost", idAlice)}

	require.NoError(t, prov.Contains(context.Background(), lookups))

	assert.False(t, deadline.IsZero(), "a call has the default timeout")
	assert.LessOrEqual(t, time.Until(deadline), valkeysegment.DefaultTimeout)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups), "a missing key is unknown by default")
	assert.Equal(
		t,
		[][]string{{"smismember semaphore:segment:{ghost} alice", "exists semaphore:segment:{ghost}"}},
		store.commands(),
	)
}
