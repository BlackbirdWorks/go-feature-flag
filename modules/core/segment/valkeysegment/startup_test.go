package valkeysegment_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
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

	two := map[string]valkeysegment.SegmentOptions{
		"a": {Key: "audience:{a}"}, "s": {Key: "restricted:{s}"},
	}
	sameNamespace := map[string]valkeysegment.SegmentOptions{
		"a": {Key: "audience:{a}"}, "b": {Key: "audience:{b}"},
	}
	smis := func(key string) string { return "smismember " + key + " " + probe }
	script := " " + valkeysegment.Script + " 1 %s " + probe
	sha := " " + valkeysegment.ScriptSHA + " 1 %s " + probe

	// more namespaces than the bound: the first ones by name are probed
	many := map[string]valkeysegment.SegmentOptions{}
	wantMany := []string{cmdPing}

	for i := range valkeysegment.MaxInitProbes + 4 {
		key := fmt.Sprintf("ns%02d:{s%02d}", i, i)
		many[fmt.Sprintf("s%02d", i)] = valkeysegment.SegmentOptions{Key: key}

		if i < valkeysegment.MaxInitProbes {
			wantMany = append(wantMany, smis(key), "exists "+key)
		}
	}

	tests := []struct {
		store         *fakeStore
		opts          *valkeysegment.Options
		name          string
		wantMsg       string
		wantPipelines [][]string
	}{
		{
			name: "two namespaces are both probed", store: &fakeStore{}, opts: &valkeysegment.Options{Segments: two},
			wantPipelines: [][]string{{
				cmdPing, smis("audience:{a}"), existsAud, smis("restricted:{s}"), existsRes,
			}},
		},
		{
			name: "segments of one namespace are probed once", store: &fakeStore{},
			opts:          &valkeysegment.Options{Segments: sameNamespace},
			wantPipelines: [][]string{{cmdPing, smis("audience:{a}"), existsAud}},
		},
		{
			name: "atomic probes the script on each namespace", store: &fakeStore{},
			opts: &valkeysegment.Options{Segments: two, Atomic: true},
			wantPipelines: [][]string{{
				cmdPing,
				"eval_ro" + fmt.Sprintf(script, "audience:{a}"), "evalsha_ro" + fmt.Sprintf(sha, "audience:{a}"),
				"eval_ro" + fmt.Sprintf(script, "restricted:{s}"), "evalsha_ro" + fmt.Sprintf(sha, "restricted:{s}"),
			}},
		},
		{
			name: "a namespace the client cannot read fails Init", store: denyingKey("restricted:"),
			opts:    &valkeysegment.Options{Segments: two},
			wantMsg: msgInit + "the command SMISMEMBER is not available: " + msgServer,
			wantPipelines: [][]string{{
				cmdPing, smis("audience:{a}"), existsAud, smis("restricted:{s}"), existsRes,
			}},
		},
		{
			name: "at most MaxInitProbes namespaces are probed", store: &fakeStore{},
			opts: &valkeysegment.Options{Segments: many}, wantPipelines: [][]string{wantMany},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prov := newProvider(t, tt.store, tt.opts)

			err := prov.Init(boundedCtx(t))

			if tt.wantMsg != "" {
				require.ErrorIs(t, err, valkeysegment.ErrServer)
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
		opts  *valkeysegment.Options
		reply func(args []any) *fakeReply
		name  string
	}{
		{
			name: "SMISMEMBER answers a string",
			opts: &valkeysegment.Options{Segments: twoSegments()},
			reply: func(args []any) *fakeReply {
				if args[0] == cmdSMIsMember {
					return &fakeReply{value: "OK"}
				}

				return nil
			},
		},
		{
			name: "SMISMEMBER answers a list of the wrong length",
			opts: &valkeysegment.Options{Segments: twoSegments()},
			reply: func(args []any) *fakeReply {
				if args[0] == cmdSMIsMember {
					return &fakeReply{value: []any{int64(0), int64(0)}}
				}

				return nil
			},
		},
		{
			name: "EXISTS answers a string",
			opts: &valkeysegment.Options{Segments: twoSegments()},
			reply: func(args []any) *fakeReply {
				if args[0] == cmdExists {
					return &fakeReply{value: "yes"}
				}

				return nil
			},
		},
		{
			name: "the script answers a string",
			opts: &valkeysegment.Options{Segments: twoSegments(), Atomic: true},
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

			require.ErrorIs(t, err, valkeysegment.ErrInit)
			require.ErrorIs(t, err, valkeysegment.ErrProtocol)
			assert.Equal(t, valkeysegment.Status{LastFailure: valkeysegment.ClassProtocol}, prov.Status())
		})
	}
}

func TestAnAtomicSetIsOneScriptWhateverItsSize(t *testing.T) {
	t.Parallel()

	const total = valkeysegment.MaxIDsPerCommand + 1

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
	prov := newProvider(t, store, &valkeysegment.Options{Segments: twoSegments(), Atomic: true})

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
	prov := newProvider(t, store, &valkeysegment.Options{Segments: twoSegments()})

	// a call that reaches no store operation neither makes the provider ready nor clears a failure
	require.NoError(t, prov.Contains(boundedCtx(t), []segment.Lookup{lk(segOther, idAlice)}))
	assert.Equal(t, valkeysegment.Status{}, prov.Status())

	down = true

	require.Error(t, prov.Contains(boundedCtx(t), []segment.Lookup{lk(segBeta, idAlice)}))

	failed := valkeysegment.Status{LastFailure: valkeysegment.ClassUnavailable}

	assert.Equal(t, failed, prov.Status())

	require.NoError(t, prov.Contains(boundedCtx(t), []segment.Lookup{lk(segOther, idAlice)}))
	assert.Equal(t, failed, prov.Status(), "a no-op call keeps the failure")

	// a successful Exists is a successful store operation
	down = false

	_, err := prov.Exists(boundedCtx(t), []string{segBeta})

	require.NoError(t, err)
	assert.Equal(t, valkeysegment.Status{Ready: true}, prov.Status())
}

func TestNegativeMaxIDBytesIsNamed(t *testing.T) {
	t.Parallel()

	_, err := valkeysegment.New(&fakeStore{}, &valkeysegment.Options{MaxIDBytes: -1})

	require.ErrorIs(t, err, valkeysegment.ErrInvalidOptions)
	assert.Equal(t, "valkey segment: invalid options: max id bytes must not be negative", err.Error())
}
