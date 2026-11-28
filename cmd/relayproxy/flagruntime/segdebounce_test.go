package flagruntime_test

import (
	"context"
	"fmt"
	"iter"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// steadyBinder binds a source whose version is different on every read: a writer that never stops.
type steadyBinder struct {
	source *segment.MemorySource
	reads  atomic.Int64
}

func (b *steadyBinder) Bind(string, segment.MissingKey, []flagruntime.BoundDefinition) (*flagruntime.Binding, error) {
	return &flagruntime.Binding{Provider: b.source, Versioner: b, Source: steadySource{b}}, nil
}

func (b *steadyBinder) Version(ctx context.Context, name string) (string, error) {
	version, err := b.source.Version(ctx, name)
	if err != nil {
		return "", fmt.Errorf("version: %w", err)
	}

	return version + "." + strconv.FormatInt(b.reads.Add(1), 10), nil
}

type steadySource struct{ *steadyBinder }

func (s steadySource) Members(ctx context.Context, name string) iter.Seq2[string, error] {
	return s.source.Members(ctx, name)
}

// debounceDoc is a snapshot segment with a version key and the version block given.
func debounceDoc(version string) string {
	return segHeader + "  - name: beta\n    members: [alice]\n" +
		"  - name: audience\n    connection: audiences\n    version: {" + version + "}\n"
}

// The debounce of a definition reaches its holder: unset is off (one pull, one version read, the set is installed even
// though it changed during the pull), a value waits for the version to go quiet and pulls again, and edits that never
// stop end the refresh at the debounce with the latest pull installed, healthy, and counted.
func TestDebounceOfADefinitionReachesItsHolder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		version         string
		wantPulls       int
		wantVersionRead int
		wantUnsettled   uint64
		wantBob         segment.State
		steady          bool
	}{
		{
			name: "unset is off: the first pull is installed", version: "key: v",
			wantPulls: 1, wantVersionRead: 1, wantBob: segment.NotMember,
		},
		{
			name: "zero is off", version: "key: v, debounce: 0s",
			wantPulls: 1, wantVersionRead: 1, wantBob: segment.NotMember,
		},
		{
			name: "one change during the pull is pulled again once quiet", version: "key: v, debounce: 2s",
			wantPulls: 2, wantVersionRead: 4, wantBob: segment.Member,
		},
		{
			name: "steady edits end at the debounce", version: "key: v, debounce: 200ms", steady: true,
			wantPulls: 2, wantUnsettled: 1, wantBob: segment.Member,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})

			var pulls atomic.Int32

			source.SetMembersHook(func(name string, yielded int) {
				if yielded != 0 || name != audienceSegmentName {
					return
				}

				if pulls.Add(1) == 1 {
					source.Set(name, []string{memberAlice, "bob"}) // the set is replaced while it is pulled
				}
			})

			var binder flagruntime.SegmentBinder = &memBinder{source: source}
			if tt.steady {
				binder = &steadyBinder{source: source}
			}

			built := buildAndCommit(t, viewBuilder(t, binder), viewContent(t, debounceDoc(tt.version)))
			set := built.Runtime.FlagSets["a"]
			require.NoError(t, set.Segments.View.RefreshVersions(t.Context(), time.Unix(1_000, 0)))

			assert.Equal(t, tt.wantPulls, source.Calls(segment.OpMembers), "pulls")

			if tt.wantVersionRead > 0 {
				assert.Equal(t, tt.wantVersionRead, source.Calls(segment.OpVersion), "version reads")
			}

			state, status, ok := set.Segments.Catalog.Answer(audienceSegmentName, memberAlice)
			require.True(t, ok)
			assert.Equal(t, segment.Member, state)
			assert.Equal(t, segment.StatusHealthy, status)

			state, _, _ = set.Segments.Catalog.Answer(audienceSegmentName, "bob")
			assert.Equal(t, tt.wantBob, state, "bob is in the set that was installed")

			conns := built.Runtime.SegmentBindings().Connections
			require.Len(t, conns, 1)
			assert.Equal(t, tt.wantUnsettled, conns[0].Unsettled)
		})
	}
}
