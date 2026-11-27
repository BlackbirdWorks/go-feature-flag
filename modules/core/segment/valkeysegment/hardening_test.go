package valkeysegment_test

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

// denying is a store that refuses the commands with a permission error, as an ACL does.
func denying(names ...string) *fakeStore {
	return &fakeStore{reply: func(args []any) *fakeReply {
		name, _ := args[0].(string)

		if slices.Contains(names, name) {
			msg := "NOPERM this user has no permissions to run the '" + name + "' command"

			return &fakeReply{err: serverError{msg: msg}}
		}

		return nil
	}}
}

func TestInit(t *testing.T) {
	t.Parallel()

	const (
		setKey  = "semaphore:segment:{beta}"
		keyID   = "flag:init-probe"
		setID   = "init-probe"
		pingCmd = cmdPing
	)

	setSegs := map[string]valkeysegment.SegmentOptions{segBeta: {}}
	keySegs := map[string]valkeysegment.SegmentOptions{"f": {Layout: valkeysegment.LayoutKeys, Key: flagPrefix}}
	bothSegs := map[string]valkeysegment.SegmentOptions{segBeta: {}, "f": keySegs["f"]}
	smis := "smismember " + setKey + " " + setID
	exists := "exists " + setKey
	existsKey := "exists " + keyID
	script := " " + valkeysegment.Script + " 1 " + setKey + " " + setID
	sha := " " + valkeysegment.ScriptSHA + " 1 " + setKey + " " + setID
	roPipe := []string{pingCmd, "eval_ro" + script, "evalsha_ro" + sha}
	plainPipe := []string{"eval" + script, "evalsha" + sha}
	atomic := &valkeysegment.Options{Segments: setSegs, Atomic: true}

	tests := []struct {
		wantErr       error
		store         *fakeStore
		opts          *valkeysegment.Options
		name          string
		wantMsg       string
		wantScriptCmd string
		wantPipelines [][]string
		wantClass     valkeysegment.Class
	}{
		{
			name: "set layout probes SMISMEMBER and EXISTS of its own key", store: &fakeStore{},
			opts:          &valkeysegment.Options{Segments: setSegs},
			wantPipelines: [][]string{{pingCmd, smis, exists}},
		},
		{
			name: "set layout without the key check probes SMISMEMBER only", store: &fakeStore{},
			opts:          &valkeysegment.Options{Segments: setSegs, MissingKey: valkeysegment.MissingNotMember},
			wantPipelines: [][]string{{pingCmd, smis}},
		},
		{
			name: "keys layout probes EXISTS under its prefix", store: &fakeStore{},
			opts:          &valkeysegment.Options{Segments: keySegs},
			wantPipelines: [][]string{{pingCmd, existsKey}},
		},
		{
			name: "both layouts probe both", store: &fakeStore{},
			opts:          &valkeysegment.Options{Segments: bothSegs},
			wantPipelines: [][]string{{pingCmd, smis, exists, existsKey}},
		},
		{
			name: "the probe is in the configured namespace", store: &fakeStore{},
			opts: &valkeysegment.Options{Segments: setSegs, KeyPrefix: "audience:"},
			wantPipelines: [][]string{{
				pingCmd, "smismember audience:{beta} " + setID, "exists audience:{beta}",
			}},
		},
		{
			name: "atomic probes the script it runs, read-only", store: &fakeStore{}, opts: atomic,
			wantPipelines: [][]string{roPipe}, wantScriptCmd: "evalsha_ro",
		},
		{
			name:          "atomic falls back to the plain script commands",
			store:         &fakeStore{unknown: map[string]bool{cmdEvalRO: true}},
			opts:          atomic,
			wantPipelines: [][]string{roPipe, plainPipe},
			wantScriptCmd: cmdEvalSHA,
		},
		{
			name: "atomic falls back when only EVALSHA_RO is refused", store: denying("evalsha_ro"),
			opts: atomic, wantPipelines: [][]string{roPipe, plainPipe}, wantScriptCmd: cmdEvalSHA,
		},
		{
			name:  "atomic without the key check uses SMISMEMBER, so it probes that",
			store: &fakeStore{},
			opts: &valkeysegment.Options{
				Segments:   setSegs,
				Atomic:     true,
				MissingKey: valkeysegment.MissingNotMember,
			},
			wantPipelines: [][]string{{pingCmd, smis}},
		},
		{
			name: "no segment, nothing to probe but the store", store: &fakeStore{}, opts: &valkeysegment.Options{},
			wantPipelines: [][]string{{pingCmd}},
		},
		{
			name: "set layout without SMISMEMBER", store: denying("smismember"),
			opts:    &valkeysegment.Options{Segments: setSegs},
			wantErr: valkeysegment.ErrServer, wantClass: valkeysegment.ClassServer,
			wantMsg:       msgInit + "the command SMISMEMBER is not available: " + msgServer,
			wantPipelines: [][]string{{pingCmd, smis, exists}},
		},
		{
			name: "set layout without EXISTS", store: denying("exists"),
			opts:    &valkeysegment.Options{Segments: setSegs},
			wantErr: valkeysegment.ErrServer, wantClass: valkeysegment.ClassServer,
			wantMsg:       msgInit + "the command EXISTS is not available: " + msgServer,
			wantPipelines: [][]string{{pingCmd, smis, exists}},
		},
		{
			name: "keys layout without EXISTS", store: denying("exists"),
			opts:    &valkeysegment.Options{Segments: keySegs},
			wantErr: valkeysegment.ErrServer, wantClass: valkeysegment.ClassServer,
			wantMsg:       msgInit + "the command EXISTS is not available: " + msgServer,
			wantPipelines: [][]string{{pingCmd, existsKey}},
		},
		{
			name: "atomic without any script command", store: denying("eval_ro", "eval"),
			opts: atomic, wantErr: valkeysegment.ErrServer, wantClass: valkeysegment.ClassServer,
			wantMsg:       msgInit + "the command EVAL is not available: " + msgServer,
			wantPipelines: [][]string{roPipe, plainPipe},
		},
		{
			name: "atomic without EVALSHA", store: denying("eval_ro", "evalsha"),
			opts: atomic, wantErr: valkeysegment.ErrServer, wantClass: valkeysegment.ClassServer,
			wantMsg:       msgInit + "the command EVALSHA is not available: " + msgServer,
			wantPipelines: [][]string{roPipe, plainPipe},
		},
		{
			name: nameStoreDown, store: &fakeStore{exec: func(context.Context) error { return errDial }},
			opts:    &valkeysegment.Options{Segments: setSegs},
			wantErr: valkeysegment.ErrUnavailable, wantClass: valkeysegment.ClassUnavailable,
			wantMsg:       "valkey segment: init failed: valkey segment: store unavailable",
			wantPipelines: [][]string{{pingCmd, smis, exists}},
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

			assert.Equal(t, tt.wantClass, prov.Status().LastFailure)
			assert.Equal(t, tt.wantPipelines, tt.store.commands())

			if tt.wantScriptCmd == "" {
				return
			}

			// the next call uses the command that Init chose
			require.NoError(t, prov.Contains(context.Background(), []segment.Lookup{lk(segBeta, idAlice)}))

			cmds := tt.store.commands()
			assert.True(t, strings.HasPrefix(cmds[len(cmds)-1][0], tt.wantScriptCmd+" "), cmds[len(cmds)-1][0])
		})
	}
}

func TestInitScriptFallbackSharesTheTimeoutOfInit(t *testing.T) {
	t.Parallel()

	var (
		mu        sync.Mutex
		deadlines []time.Time
	)

	store := &fakeStore{
		unknown: map[string]bool{cmdEvalRO: true},
		exec: func(ctx context.Context) error {
			deadline, _ := ctx.Deadline()

			mu.Lock()
			defer mu.Unlock()

			deadlines = append(deadlines, deadline)

			return nil
		},
	}
	prov := newProvider(t, store, &valkeysegment.Options{
		Segments: map[string]valkeysegment.SegmentOptions{segBeta: {}}, Atomic: true,
	})

	require.NoError(t, prov.Init(boundedCtx(t)))
	require.Len(t, store.commands(), 2, "the read-only probe was refused, so the plain one ran")

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, deadlines, 2)
	assert.False(t, deadlines[0].IsZero())
	assert.Equal(t, deadlines[0], deadlines[1], "one timeout for the whole of Init")
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
	require.NoError(t, prov.Shutdown(boundedCtx(t)))
	require.NoError(t, prov.Shutdown(boundedCtx(t)))
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
		{name: "a segment that is not configured", store: &fakeStore{}, id: idAlice, seg: segOther},
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
	unreachableAddr  = "127.0.0.1:1"
	nameStoreDown    = "the store is down"
	msgInit          = "valkey segment: init failed: "
	msgServer        = "valkey segment: store returned an error"
	classUnavailable = "unavailable"
	cmdPing          = "ping"
)
