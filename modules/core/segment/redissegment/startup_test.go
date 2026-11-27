package redissegment_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// denyingKey is a store that refuses every command on a key with the prefix, as an ACL on a namespace does.
func denyingKey(prefix string) *fakeStore {
	return &fakeStore{reply: func(args []any) *fakeReply {
		for _, arg := range args[1:] {
			if key, _ := arg.(string); strings.HasPrefix(key, prefix) {
				return &fakeReply{err: serverError{msg: "NOPERM no permissions to access the key"}}
			}
		}

		return nil
	}}
}

func TestInitProbesEveryNamespace(t *testing.T) {
	t.Parallel()

	const (
		probe     = "init-probe"
		existsAud = "exists audience:{a}"
		existsRes = "exists restricted:{s}"
	)

	two := map[string]redissegment.SegmentOptions{
		"a": {Key: "audience:{a}"}, "s": {Key: "restricted:{s}"},
	}
	sameNamespace := map[string]redissegment.SegmentOptions{
		"a": {Key: "audience:{a}"}, "b": {Key: "audience:{b}"},
	}
	smis := func(key string) string { return "smismember " + key + " " + probe }
	script := " " + redissegment.Script + " 1 %s " + probe
	sha := " " + redissegment.ScriptSHA + " 1 %s " + probe

	// more namespaces than the bound: the first ones by name are probed
	many := map[string]redissegment.SegmentOptions{}
	wantMany := []string{cmdPing}

	for i := range redissegment.MaxInitProbes + 4 {
		key := fmt.Sprintf("ns%02d:{s%02d}", i, i)
		many[fmt.Sprintf("s%02d", i)] = redissegment.SegmentOptions{Key: key}

		if i < redissegment.MaxInitProbes {
			wantMany = append(wantMany, smis(key), "exists "+key)
		}
	}

	// both layouts: the cap is shared, and the first namespaces by name are probed
	mixed := map[string]redissegment.SegmentOptions{}
	wantMixed := make([]string, 1, 23)
	wantMixed[0] = cmdPing

	for i := range 10 {
		mixed[fmt.Sprintf("k%02d", i)] = redissegment.SegmentOptions{
			Layout: redissegment.LayoutKeys, Key: fmt.Sprintf("kp%02d:", i),
		}
		mixed[fmt.Sprintf("s%02d", i)] = redissegment.SegmentOptions{Key: fmt.Sprintf("sp%02d:{s%02d}", i, i)}
	}

	// by name: k00 to k09 first, then s00 to s05; the sets are sent before the keys
	for i := range 6 {
		key := fmt.Sprintf("sp%02d:{s%02d}", i, i)
		wantMixed = append(wantMixed, smis(key), "exists "+key)
	}

	for i := range 10 {
		wantMixed = append(wantMixed, fmt.Sprintf("exists kp%02d:%s", i, probe))
	}

	tests := []struct {
		store         *fakeStore
		opts          *redissegment.Options
		name          string
		wantMsg       string
		wantPipelines [][]string
	}{
		{
			name:  "two namespaces are both probed",
			store: &fakeStore{},
			opts:  &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: two},
			wantPipelines: [][]string{{
				cmdPing, smis("audience:{a}"), existsAud, smis("restricted:{s}"), existsRes,
			}},
		},
		{
			name: "segments of one namespace are probed once", store: &fakeStore{},
			opts:          &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: sameNamespace},
			wantPipelines: [][]string{{cmdPing, smis("audience:{a}"), existsAud}},
		},
		{
			name: "atomic probes the script on each namespace", store: &fakeStore{},
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: two, Atomic: true},
			wantPipelines: [][]string{{
				cmdPing,
				"eval_ro" + fmt.Sprintf(script, "audience:{a}"), "evalsha_ro" + fmt.Sprintf(sha, "audience:{a}"),
				"eval_ro" + fmt.Sprintf(script, "restricted:{s}"), "evalsha_ro" + fmt.Sprintf(sha, "restricted:{s}"),
			}},
		},
		{
			name: "a namespace the client cannot read fails Init", store: denyingKey("restricted:"),
			opts:    &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: two},
			wantMsg: msgInit + "the command SMISMEMBER is not available: " + msgServer,
			wantPipelines: [][]string{{
				cmdPing, smis("audience:{a}"), existsAud, smis("restricted:{s}"), existsRes,
			}},
		},
		{
			name:  "the cap is shared by the two layouts",
			store: &fakeStore{},
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingUnknown,
				Segments:   mixed,
			},
			wantPipelines: [][]string{wantMixed},
		},
		{
			name:  "at most MaxInitProbes namespaces are probed",
			store: &fakeStore{},
			opts: &redissegment.Options{
				MissingKey: redissegment.MissingUnknown,
				Segments:   many,
			},
			wantPipelines: [][]string{wantMany},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(t, tt.store, tt.opts)

			err := prov.Init(boundedCtx(t))

			if tt.wantMsg != "" {
				require.ErrorIs(t, err, redissegment.ErrServer)
				assert.Equal(t, tt.wantMsg, err.Error())
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantPipelines, tt.store.commands())
		})
	}
}

func TestInitChecksTheShapeOfTheReplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		opts  *redissegment.Options
		reply func(args []any) *fakeReply
		name  string
	}{
		{
			name: "SMISMEMBER answers a string",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			reply: func(args []any) *fakeReply {
				if args[0] == cmdSMIsMember {
					return &fakeReply{value: "OK"}
				}

				return nil
			},
		},
		{
			name: "SMISMEMBER answers a list of the wrong length",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			reply: func(args []any) *fakeReply {
				if args[0] == cmdSMIsMember {
					return &fakeReply{value: []any{int64(0), int64(0)}}
				}

				return nil
			},
		},
		{
			name: "EXISTS answers a string",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments()},
			reply: func(args []any) *fakeReply {
				if args[0] == cmdExists {
					return &fakeReply{value: "yes"}
				}

				return nil
			},
		},
		{
			name: "the script answers a string",
			opts: &redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments(), Atomic: true},
			reply: func(args []any) *fakeReply {
				if name, _ := args[0].(string); strings.HasPrefix(name, "eval") {
					return &fakeReply{value: "OK"}
				}

				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(t, &fakeStore{reply: tt.reply}, tt.opts)

			err := prov.Init(boundedCtx(t))

			require.ErrorIs(t, err, redissegment.ErrInit)
			require.ErrorIs(t, err, redissegment.ErrProtocol)
			assert.Equal(t, redissegment.Status{LastFailure: redissegment.ClassProtocol}, prov.Status())
		})
	}
}

func TestAnAtomicSetIsOneScriptWhateverItsSize(t *testing.T) {
	t.Parallel()

	const total = redissegment.MaxIDsPerCommand + 1

	lookups := make([]segment.Lookup, total)
	want := make([]segment.State, total)
	members := map[string]bool{}

	for i := range total {
		id := fmt.Sprintf("id%d", i)
		lookups[i] = lk(segBeta, id)
		members[id] = true
		want[i] = segment.Member
	}

	store := &fakeStore{sets: map[string]map[string]bool{keyBeta: members}}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments(), Atomic: true},
	)

	require.NoError(t, prov.Contains(boundedCtx(t), lookups))
	assert.Equal(t, want, states(lookups))

	// the first call loads the script: one command by SHA, then one with the text
	pipelines := store.commands()
	require.Len(t, pipelines, 2)
	assert.Len(t, pipelines[0], 1, "one script for the set, not one per MaxIDsPerCommand ids")
	assert.Len(t, pipelines[1], 1)
}

func TestHealthFollowsTheStoreOperationsOnly(t *testing.T) {
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

	// a call that reaches no store operation neither makes the provider ready nor clears a failure
	require.NoError(t, prov.Contains(boundedCtx(t), []segment.Lookup{lk(segOther, idAlice)}))
	assert.Equal(t, redissegment.Status{}, prov.Status())

	down = true

	require.Error(t, prov.Contains(boundedCtx(t), []segment.Lookup{lk(segBeta, idAlice)}))

	failed := redissegment.Status{LastFailure: redissegment.ClassUnavailable}

	assert.Equal(t, failed, prov.Status())

	require.NoError(t, prov.Contains(boundedCtx(t), []segment.Lookup{lk(segOther, idAlice)}))
	assert.Equal(t, failed, prov.Status(), "a no-op call keeps the failure")

	// a successful Exists is a successful store operation
	down = false

	_, err := prov.Exists(boundedCtx(t), []string{segBeta})

	require.NoError(t, err)
	assert.Equal(t, redissegment.Status{Ready: true}, prov.Status())
}

func TestNegativeMaxIDBytesIsNamed(t *testing.T) {
	t.Parallel()

	_, err := redissegment.New(
		&fakeStore{},
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, MaxIDBytes: -1},
	)

	require.ErrorIs(t, err, redissegment.ErrInvalidOptions)
	assert.Equal(t, "redis segment: invalid options: max id bytes must not be negative", err.Error())
}

func TestAParseErrorOfTheScriptRetryTrustsNothing(t *testing.T) {
	t.Parallel()

	var execs atomic.Int32

	store := &fakeStore{
		sets: failureSetBeta(),
		exec: func(context.Context) error {
			if execs.Add(1) == 2 { // the pipeline that sends the script again
				return errParse()
			}

			return nil
		},
		reply: func(args []any) *fakeReply {
			// the store has the script for the first set only
			if name, _ := args[0].(string); name == cmdEvalSHA && args[3] == keyChurned {
				return &fakeReply{err: serverError{msg: "NOSCRIPT No matching script"}}
			}

			return nil
		},
	}
	prov := newProvider(
		t,
		store,
		&redissegment.Options{MissingKey: redissegment.MissingUnknown, Segments: twoSegments(), Atomic: true},
	)
	lookups := []segment.Lookup{lk(segBeta, idAlice), lk(segChurned, carolID)}

	err := prov.Contains(boundedCtx(t), lookups)

	require.ErrorIs(t, err, redissegment.ErrUnavailable)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, states(lookups))
	assert.Len(t, store.commands(), 2, "the script was sent again")
}
