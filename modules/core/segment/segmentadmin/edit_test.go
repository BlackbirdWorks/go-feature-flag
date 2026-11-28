package segmentadmin_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

const lineBreakID = "a\nb"

const memberPrefix = "semaphore:segment:members:"

// editCase is one row of TestEdit.
type editCase struct {
	wantErr       error
	name          string
	wantText      string
	failOp        string
	loseOp        string
	input         string
	preload       []string
	wantSet       []string
	wantKeys      []string
	args          []string
	wantRejected  segmentadmin.Rejections
	wantAdded     int64
	failNth       int
	batch         int
	wantRemoved   int64
	lockHeld      bool
	failAll       bool
	keys          bool
	remove        bool
	wantVersioned bool
}

// prepare builds the store, the target and the input of a row.
func (tt *editCase) prepare(t *testing.T) (*admintest.Fake, *segmentadmin.Target, *testSource) {
	t.Helper()

	store := admintest.New()
	if len(tt.preload) > 0 {
		_, err := store.SAdd(t.Context(), liveKey, tt.preload, 0)
		require.NoError(t, err)
	}

	target := vipTarget()
	target.Normalize = resolver.NormalizeTrimLower

	if tt.keys {
		target = &segmentadmin.Target{Key: memberPrefix, Layout: redissegment.LayoutKeys, VersionKey: versionKey}

		if tt.remove {
			_, err := store.SetKeys(t.Context(), []string{memberPrefix + "a"})
			require.NoError(t, err)
		}
	}

	switch {
	case tt.failOp != "" && tt.failAll:
		store.FailFrom(tt.failOp, 1)
	case tt.failOp != "":
		store.Fail(tt.failOp, max(tt.failNth, 1))
	}

	if tt.loseOp != "" {
		store.LoseReply(tt.loseOp, 1, false)
	}

	if tt.lockHeld {
		store.Put(lockKey, otherToken)
	}

	if tt.args != nil {
		return store, target, &testSource{Source: segmentadmin.NewSliceSource(tt.args)}
	}

	reader, err := segmentadmin.NewReader(strings.NewReader(tt.input), segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	return store, target, &testSource{Source: reader}
}

// testSource is the input of a row: a file or the arguments.
type testSource struct {
	segmentadmin.Source
}

func editCases() []editCase {
	return []editCase{
		{
			name: "add to a set", input: inputAB, preload: []string{"x"}, wantSet: []string{"a", "b", "x"},
			wantAdded: 2, wantRejected: segmentadmin.Rejections{}, wantVersioned: true,
		},
		{
			name: "add in batches", input: inputABC, batch: 2, wantSet: []string{"a", "b", "c"}, wantAdded: 3,
			wantRejected: segmentadmin.Rejections{}, wantVersioned: true,
		},
		{
			name: "remove from a set", input: "a\nzz\n\n", remove: true, preload: []string{"a", "b"},
			wantSet: []string{"b"}, wantRemoved: 1, wantVersioned: true,
			wantRejected: segmentadmin.Rejections{Empty: 1},
		},
		{
			name:          "add member keys",
			input:         inputAB,
			keys:          true,
			wantKeys:      []string{memberPrefix + "a", memberPrefix + "b"},
			wantAdded:     2,
			wantRejected:  segmentadmin.Rejections{},
			wantVersioned: true,
		},
		{
			name: "remove member keys", input: "a\n", keys: true, remove: true, wantRemoved: 1,
			wantRejected: segmentadmin.Rejections{}, wantVersioned: true,
		},
		{
			name:         "nothing accepted changes nothing",
			input:        "\n",
			wantRejected: segmentadmin.Rejections{Empty: 1},
		},
		{
			name: "a failed first batch may have been applied: the version is bumped", input: "a\n", failOp: opEDIT,
			wantErr: segmentadmin.ErrPartial, wantText: "0 of 1 ids confirmed", wantVersioned: true,
		},
		{
			name: "a lost reply may hide an applied batch: the version is bumped", input: "a\n", loseOp: opEDIT,
			wantErr: segmentadmin.ErrPartial, wantSet: []string{"a"}, wantVersioned: true,
		},
		{
			name: "an edit while an import holds the lock is refused and nothing is bumped", input: "a\n",
			lockHeld: true, preload: []string{"x"}, wantErr: segmentadmin.ErrImportRunning, wantSet: []string{"x"},
		},
		{
			name: "a failed bump is reported", input: "a\n", failOp: opSET, failAll: true,
			wantErr: segmentadmin.ErrNotBumped,
			wantSet: []string{"a"}, wantAdded: 1, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "ids of the command line are never joined", args: []string{lineBreakID, "ok", "t\x00"},
			wantSet: []string{"ok"}, wantAdded: 1, wantRejected: segmentadmin.Rejections{Invalid: 2},
			wantVersioned: true,
		},
		{
			name:          "a failure after a batch bumps the version and says how much was applied",
			input:         inputABC,
			batch:         1,
			failOp:        opEDIT,
			failNth:       2,
			wantErr:       segmentadmin.ErrPartial,
			wantText:      "1 of 2 ids confirmed",
			wantSet:       []string{"a"},
			wantRejected:  segmentadmin.Rejections{},
			wantVersioned: true,
		},
		{name: "bad batch size", input: "a\n", batch: -1, wantErr: segmentadmin.ErrBadOptions},
	}
}

func TestStat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		want      segmentadmin.Stats
		limit     int
		keys      bool
		noVersion bool
	}{
		{
			name: "a set",
			want: segmentadmin.Stats{Exists: true, Count: 3, Version: "v1", HasVersion: true, TTL: time.Minute},
		},
		{
			name:      "a set without a version key",
			noVersion: true,
			want:      segmentadmin.Stats{Exists: true, Count: 3, TTL: time.Minute},
		},
		{
			name:  "member keys",
			keys:  true,
			limit: 10,
			want:  segmentadmin.Stats{Exists: true, Count: 2, Version: "v1", HasVersion: true},
		},
		{
			name:  "member keys over the bound",
			keys:  true,
			limit: 1,
			want:  segmentadmin.Stats{Exists: true, Count: 1, AtLeast: true, Version: "v1", HasVersion: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			ctx := t.Context()

			_, err := store.SAdd(ctx, liveKey, []string{"a", "b", "c"}, time.Minute)
			require.NoError(t, err)
			_, err = store.SetKeys(ctx, []string{memberPrefix + "a", memberPrefix + "b"})
			require.NoError(t, err)
			require.NoError(t, store.SetValue(ctx, versionKey, "v1"))

			target := vipTarget()
			if tt.keys {
				target = &segmentadmin.Target{
					Key:        memberPrefix,
					Layout:     redissegment.LayoutKeys,
					VersionKey: versionKey,
				}
			}

			if tt.noVersion {
				target.VersionKey = ""
			}

			got, err := segmentadmin.Stat(ctx, store, target, tt.limit)
			require.NoError(t, err)
			assert.Equal(t, tt.want, *got)
		})
	}
}

func TestEdit(t *testing.T) {
	t.Parallel()

	for _, tt := range editCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, target, source := tt.prepare(t)

			rep, err := segmentadmin.Edit(t.Context(), store, target, source, tt.remove,
				segmentadmin.EditOptions{BatchSize: tt.batch, Wait: instant})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.ErrorContains(t, err, tt.wantText)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantAdded, rep.Added)
				assert.Equal(t, tt.wantRemoved, rep.Removed)
				assert.Equal(t, tt.wantRejected, rep.Rejected)
			}

			if !tt.keys {
				assert.Equal(t, tt.wantSet, nonNil(store.Members(liveKey)))
			}

			if tt.wantKeys != nil {
				assert.Subset(t, store.Keys(), tt.wantKeys)
			}

			_, ok, _ := store.Value(t.Context(), versionKey)
			assert.Equal(t, tt.wantVersioned, ok)
		})
	}
}

func TestStat_failures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		failOp string
		keys   bool
	}{
		{name: "ttl", failOp: "PTTL"},
		{name: "cardinality", failOp: "SCARD"},
		{name: "scan", failOp: "SCAN", keys: true},
		{name: "version", failOp: opGET},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			_, err := store.SAdd(t.Context(), liveKey, []string{"a"}, 0)
			require.NoError(t, err)

			store.Fail(tt.failOp, 1)

			target := vipTarget()
			if tt.keys {
				target = &segmentadmin.Target{Key: memberPrefix, Layout: redissegment.LayoutKeys}
			}

			_, err = segmentadmin.Stat(t.Context(), store, target, 0)
			require.ErrorIs(t, err, admintest.ErrInjected)
		})
	}
}
