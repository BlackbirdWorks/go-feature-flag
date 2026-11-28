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

const memberPrefix = "semaphore:segment:flags:"

func TestEdit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr       error
		name          string
		wantText      string
		failOp        string
		input         string
		wantSet       []string
		wantKeys      []string
		args          []string
		preload       []string
		wantRejected  segmentadmin.Rejections
		failNth       int
		batch         int
		wantAdded     int64
		wantRemoved   int64
		keys          bool
		remove        bool
		wantVersioned bool
	}{
		{
			name: "add to a set", input: "a\nb\n", preload: []string{"x"}, wantSet: []string{"a", "b", "x"},
			wantAdded: 2, wantRejected: segmentadmin.Rejections{}, wantVersioned: true,
		},
		{
			name: "add in batches", input: "a\nb\nc\n", batch: 2, wantSet: []string{"a", "b", "c"}, wantAdded: 3,
			wantRejected: segmentadmin.Rejections{}, wantVersioned: true,
		},
		{
			name: "remove from a set", input: "a\nzz\n\n", remove: true, preload: []string{"a", "b"},
			wantSet: []string{"b"}, wantRemoved: 1, wantVersioned: true,
			wantRejected: segmentadmin.Rejections{Empty: 1},
		},
		{
			name:          "add member keys",
			input:         "a\nb\n",
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
			name: "a failed batch is an error", input: "a\n", failOp: opSADD, wantErr: admintest.ErrInjected,
			wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a failed bump is reported", input: "a\n", failOp: opSET, wantErr: segmentadmin.ErrSwapped,
			wantSet: []string{"a"}, wantAdded: 1, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "ids of the command line are never joined", args: []string{lineBreakID, "ok", "t\x00"},
			wantSet: []string{"ok"}, wantAdded: 1, wantRejected: segmentadmin.Rejections{Invalid: 2},
			wantVersioned: true,
		},
		{
			name:          "a failure after a batch bumps the version and says how much was applied",
			input:         "a\nb\nc\n",
			batch:         1,
			failOp:        opSADD,
			failNth:       2,
			wantErr:       segmentadmin.ErrPartial,
			wantText:      "1 of 2 ids applied",
			wantSet:       []string{"a"},
			wantRejected:  segmentadmin.Rejections{},
			wantVersioned: true,
		},
		{name: "bad batch size", input: "a\n", batch: -1, wantErr: segmentadmin.ErrBadOptions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			if len(tt.preload) > 0 {
				_, err := store.SAdd(t.Context(), liveKey, tt.preload, 0)
				require.NoError(t, err)
			}

			target := vipTarget()

			target.Normalize = resolver.NormalizeTrimLower
			if tt.keys {
				target = &segmentadmin.Target{
					Key: memberPrefix, Layout: redissegment.LayoutKeys, VersionKey: versionKey,
				}
				if tt.remove {
					_, err := store.SetKeys(t.Context(), []string{memberPrefix + "a"})
					require.NoError(t, err)
				}
			}

			if tt.failOp != "" {
				store.Fail(tt.failOp, max(tt.failNth, 1))
			}

			reader, err := segmentadmin.NewReader(strings.NewReader(tt.input), segmentadmin.ReaderOptions{})
			require.NoError(t, err)

			var source segmentadmin.Source = reader
			if tt.args != nil {
				source = segmentadmin.NewSliceSource(tt.args)
			}

			rep, err := segmentadmin.Edit(t.Context(), store, target, source, tt.remove, tt.batch)
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
