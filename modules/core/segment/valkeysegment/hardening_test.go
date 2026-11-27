package valkeysegment_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

func TestScriptSHAIsTheSHAOfTheScript(t *testing.T) {
	t.Parallel()

	sum := sha1.Sum([]byte(valkeysegment.Script))

	assert.Equal(t, valkeysegment.ScriptSHA, hex.EncodeToString(sum[:]))
}

func TestScriptIsLoadedOnceAndThenCalledBySHA(t *testing.T) {
	t.Parallel()

	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: {idAlice: true}}}
	prov := newProvider(t, store, &valkeysegment.Options{Atomic: true, Segments: twoSegments()})
	bySHA := "evalsha " + valkeysegment.ScriptSHA + " 1 " + keyBeta + " alice"
	full := "eval " + valkeysegment.Script + " 1 " + keyBeta + " alice"

	for range 3 {
		lookups := []segment.Lookup{lk(segBeta, idAlice)}

		require.NoError(t, prov.Contains(context.Background(), lookups))
		assert.Equal(t, []segment.State{segment.Member}, states(lookups))
	}

	assert.Equal(t, [][]string{{bySHA}, {full}, {bySHA}, {bySHA}}, store.commands(),
		"the first call loads the script, the next ones name it")
}

func TestInit(t *testing.T) {
	t.Parallel()

	setSegs := map[string]valkeysegment.SegmentOptions{segBeta: {}}
	keySegs := map[string]valkeysegment.SegmentOptions{"f": {Layout: valkeysegment.LayoutKeys, Key: flagPrefix}}
	probe := "semaphore:segment:{init-probe}"

	tests := []struct {
		wantErr      error
		store        *fakeStore
		opts         *valkeysegment.Options
		name         string
		wantMsg      string
		wantReadOnly string
		wantCommands []string
	}{
		{
			name:         "set layout probes SMISMEMBER and learns the read-only scripts",
			store:        &fakeStore{},
			opts:         &valkeysegment.Options{Segments: setSegs},
			wantCommands: []string{cmdPing, "smismember " + probe + " x", cmdEvalROProbe},
			wantReadOnly: "evalsha_ro",
		},
		{
			name:         "keys layout probes EXISTS",
			store:        &fakeStore{},
			opts:         &valkeysegment.Options{Segments: keySegs},
			wantCommands: []string{cmdPing, "exists " + probe, cmdEvalROProbe},
		},
		{
			name:         "atomic probes EVAL",
			store:        &fakeStore{},
			opts:         &valkeysegment.Options{Segments: setSegs, Atomic: true},
			wantCommands: []string{cmdPing, "smismember " + probe + " x", cmdEvalProbe, cmdEvalROProbe},
			wantReadOnly: "evalsha_ro",
		},
		{
			name:         "a store without the read-only scripts uses the plain ones",
			store:        &fakeStore{unknown: map[string]bool{"eval_ro": true}},
			opts:         &valkeysegment.Options{Segments: setSegs, Atomic: true},
			wantCommands: []string{cmdPing, "smismember " + probe + " x", cmdEvalProbe, cmdEvalROProbe},
			wantReadOnly: "evalsha",
		},
		{
			name:         "a store without SMISMEMBER is found at the start",
			store:        &fakeStore{unknown: map[string]bool{"smismember": true}},
			opts:         &valkeysegment.Options{Segments: setSegs},
			wantErr:      valkeysegment.ErrInit,
			wantMsg:      msgInit + "the command SMISMEMBER is not available: " + msgServer,
			wantCommands: []string{cmdPing, "smismember " + probe + " x", cmdEvalROProbe},
		},
		{
			name:         "a store without EVAL is found when Atomic needs it",
			store:        &fakeStore{unknown: map[string]bool{"eval": true}},
			opts:         &valkeysegment.Options{Segments: setSegs, Atomic: true},
			wantErr:      valkeysegment.ErrInit,
			wantMsg:      msgInit + "the command EVAL is not available: " + msgServer,
			wantCommands: []string{cmdPing, "smismember " + probe + " x", cmdEvalProbe, cmdEvalROProbe},
		},
		{
			name:         "a store that is down",
			store:        &fakeStore{exec: func(context.Context) error { return errDial }},
			opts:         &valkeysegment.Options{Segments: setSegs},
			wantErr:      valkeysegment.ErrUnavailable,
			wantMsg:      "valkey segment: init failed: valkey segment: store unavailable",
			wantCommands: []string{cmdPing, "smismember " + probe + " x", cmdEvalROProbe},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(t, tt.store, tt.opts)

			err := prov.Init(context.Background())

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorIs(t, err, valkeysegment.ErrInit)
				assert.Equal(t, tt.wantMsg, err.Error())
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, [][]string{tt.wantCommands}, tt.store.commands())

			if tt.wantReadOnly == "" {
				return
			}

			// the next call uses the command that Init chose; the script is not cached, so the call loads it
			lookups := []segment.Lookup{lk(segBeta, idAlice)}
			opts := *tt.opts
			opts.Atomic = true
			after := newProvider(t, tt.store, &opts)

			require.NoError(t, after.Init(context.Background()))
			require.NoError(t, after.Contains(context.Background(), lookups))

			cmds := tt.store.commands()
			assert.True(t, strings.HasPrefix(cmds[len(cmds)-2][0], tt.wantReadOnly+" "), cmds[len(cmds)-2][0])
		})
	}
}

func TestOversizedIDsAreNeverSent(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", valkeysegment.DefaultMaxIDBytes+1)
	exact := strings.Repeat("b", valkeysegment.DefaultMaxIDBytes)

	tests := []struct {
		opts         *valkeysegment.Options
		name         string
		lookups      []segment.Lookup
		wantStates   []segment.State
		wantSent     []string
		wantObserved int
	}{
		{
			name:         "one byte over the default",
			opts:         &valkeysegment.Options{Segments: twoSegments(), MissingKey: valkeysegment.MissingNotMember},
			lookups:      []segment.Lookup{lk(segBeta, long), lk(segBeta, idAlice)},
			wantStates:   []segment.State{segment.Unknown, segment.Member},
			wantObserved: 1,
			wantSent:     []string{cmdBeta + " alice"},
		},
		{
			name:         "exactly the default is looked up",
			opts:         &valkeysegment.Options{Segments: twoSegments(), MissingKey: valkeysegment.MissingNotMember},
			lookups:      []segment.Lookup{lk(segBeta, exact)},
			wantStates:   []segment.State{segment.NotMember},
			wantObserved: 0,
			wantSent:     []string{cmdBeta + " " + exact},
		},
		{
			name: "a custom limit",
			opts: &valkeysegment.Options{
				Segments: twoSegments(), MissingKey: valkeysegment.MissingNotMember, MaxIDBytes: 4,
			},
			lookups:      []segment.Lookup{lk(segBeta, "alice"), lk(segBeta, "bob")},
			wantStates:   []segment.State{segment.Unknown, segment.NotMember},
			wantObserved: 1,
			wantSent:     []string{cmdBeta + " bob"},
		},
		{
			name: "the keys layout too",
			opts: &valkeysegment.Options{
				Segments: map[string]valkeysegment.SegmentOptions{
					"f": {Layout: valkeysegment.LayoutKeys, Key: flagPrefix},
				},
				MaxIDBytes: 3,
			},
			lookups:      []segment.Lookup{lk("f", "alice"), lk("f", "bob")},
			wantStates:   []segment.State{segment.Unknown, segment.Member},
			wantObserved: 1,
			wantSent:     []string{"exists flag:bob"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				sets: map[string]map[string]bool{keyBeta: {idAlice: true}},
				keys: map[string]bool{"flag:bob": true},
			}
			rec := &recorder{}
			opts := *tt.opts
			opts.Observer = rec
			prov := newProvider(t, store, &opts)

			require.NoError(t, prov.Contains(context.Background(), tt.lookups))

			got := rec.all()
			require.Len(t, got, 1)
			assert.Equal(t, tt.wantStates, states(tt.lookups))
			assert.Equal(t, tt.wantObserved, got[0].Oversized)
			assert.Equal(t, [][]string{tt.wantSent}, store.commands())
		})
	}

	_, err := valkeysegment.New(&fakeStore{}, &valkeysegment.Options{MaxIDBytes: -1})
	require.ErrorIs(t, err, valkeysegment.ErrInvalidOptions)
}

func TestCloseAndShutdownAreIdempotent(t *testing.T) {
	t.Parallel()

	prov, err := valkeysegment.Dial(&redis.UniversalOptions{Addrs: []string{unreachableAddr}}, &valkeysegment.Options{})
	require.NoError(t, err)

	require.NoError(t, prov.Close())
	require.NoError(t, prov.Close())
	require.NoError(t, prov.Shutdown(context.Background()))
	require.NoError(t, prov.Shutdown(context.Background()))
	assert.True(t, prov.Health().Closed)
}

func TestAStateLeftInTheSliceNeverSurvivesAFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		store *fakeStore
		opts  *valkeysegment.Options
		name  string
		id    string
		seg   string
	}{
		{
			name:  nameStoreDown,
			store: &fakeStore{exec: func(context.Context) error { return errDial }},
			id:    idAlice,
			seg:   segBeta,
		},
		{
			name:  "an oversized id",
			store: &fakeStore{},
			opts:  &valkeysegment.Options{MaxIDBytes: 2},
			id:    idAlice,
			seg:   segBeta,
		},
		{name: "a segment that is not configured", store: &fakeStore{}, id: idAlice, seg: "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := valkeysegment.Options{}
			if tt.opts != nil {
				opts = *tt.opts
			}

			opts.Segments = twoSegments()
			prov := newProvider(t, tt.store, &opts)
			lookups := []segment.Lookup{
				{
					Segment: tt.seg,
					ID:      tt.id,
					State:   segment.Member,
				},
				{Segment: tt.seg, ID: tt.id, State: segment.NotMember},
			}

			_ = prov.Contains(context.Background(), lookups)

			assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, states(lookups))
		})
	}
}

func TestALargeSegmentIsSeveralCommandsInOnePipeline(t *testing.T) {
	t.Parallel()

	const total = 2500

	members := map[string]bool{}
	lookups := make([]segment.Lookup, total)
	want := make([]segment.State, total)

	for i := range total {
		id := "id" + strconv.Itoa(i)
		lookups[i] = lk(segBeta, id)
		want[i] = segment.NotMember

		if i%7 == 0 {
			members[id] = true
			want[i] = segment.Member
		}
	}

	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: members}}
	prov := newProvider(
		t,
		store,
		&valkeysegment.Options{Segments: twoSegments(), MissingKey: valkeysegment.MissingNotMember},
	)

	require.NoError(t, prov.Contains(context.Background(), lookups))
	assert.Equal(t, want, states(lookups))

	pipelines := store.commands()
	require.Len(t, pipelines, 1, "one pipeline")

	sizes := make([]int, len(pipelines[0]))
	for i, cmd := range pipelines[0] {
		sizes[i] = len(strings.Fields(cmd)) - 2 // the command and the key
	}

	assert.Equal(
		t,
		[]int{valkeysegment.MaxIDsPerCommand, valkeysegment.MaxIDsPerCommand, total - 2*valkeysegment.MaxIDsPerCommand},
		sizes,
	)
}

func TestHealth(t *testing.T) {
	t.Parallel()

	down := true
	store := &fakeStore{
		sets: map[string]map[string]bool{keyBeta: {idAlice: true}},
		exec: func(context.Context) error {
			if down {
				return errDial
			}

			return nil
		},
	}
	prov := newProvider(t, store, &valkeysegment.Options{Segments: twoSegments()})

	var reporter segment.HealthReporter = prov

	assert.Equal(t, segment.Health{}, reporter.Health())

	require.Error(t, prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, segment.Health{Failure: classUnavailable}, reporter.Health())

	down = false

	require.NoError(t, prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))
	assert.Equal(t, segment.Health{Ready: true}, reporter.Health())
}

const (
	cmdEvalROProbe   = "eval_ro return 1 0"
	cmdEvalProbe     = "eval return 1 0"
	unreachableAddr  = "127.0.0.1:1"
	nameStoreDown    = "the store is down"
	msgInit          = "valkey segment: init failed: "
	msgServer        = "valkey segment: store returned an error"
	classUnavailable = "unavailable"
	cmdPing          = "ping"
)
