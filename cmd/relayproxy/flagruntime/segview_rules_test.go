package flagruntime_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const tenantAttribute = "tenant"

// The interval of a group is the shortest effective interval of its definitions: the default of five seconds only
// stands for a definition that gives none, so an explicit minute alone is a minute.
func TestSegmentViewPollInterval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		doc       string
		at        []time.Duration
		wantCalls int
	}{
		{
			name: "a minute alone", doc: lookupDoc + "    version: {key: v1, interval: 1m}\n",
			at: []time.Duration{0, 6 * time.Second, 59 * time.Second, 61 * time.Second}, wantCalls: 2,
		},
		{
			name: "a minute and the default",
			doc:  lookupDoc + "    version: {key: v1, interval: 1m}\n" + sprintOther(""),
			at:   []time.Duration{0, 4 * time.Second, 6 * time.Second}, wantCalls: 4,
		},
		{
			name: "a minute and ten seconds",
			doc:  lookupDoc + "    version: {key: v1, interval: 1m}\n" + sprintOther(", interval: 10s"),
			at:   []time.Duration{0, 6 * time.Second, 11 * time.Second}, wantCalls: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			view := viewSet(t, &memBinder{source: source}, tt.doc).Segments.View

			for _, at := range tt.at {
				require.NoError(t, view.RefreshVersions(t.Context(), time.Unix(1_000, 0).Add(at)))
			}

			assert.Equal(t, tt.wantCalls, source.Calls(segment.OpVersion))
		})
	}
}

func sprintOther(interval string) string {
	return "  - name: other\n    connection: audiences\n    mode: lookup\n    version: {key: v2" + interval + "}\n"
}

// A view answers only the segments it serves: a lookup of another name stays Unknown, although the store would answer
// it.
func TestSegmentViewContains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		segment   string
		id        string
		wantState segment.State
	}{
		{name: "served member", segment: audienceSegmentName, id: memberAlice, wantState: segment.Member},
		{name: "served not a member", segment: audienceSegmentName, id: memberBob, wantState: segment.NotMember},
		{name: "unserved", segment: "ghost", id: memberAlice, wantState: segment.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// the in-memory source answers NotMember for a segment it does not hold
			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			view := viewSet(t, &memBinder{source: source}, lookupDoc).Segments.View

			lookups := []segment.Lookup{{Segment: tt.segment, ID: tt.id}}

			require.NoError(t, view.Contains(t.Context(), lookups))
			assert.Equal(t, tt.wantState, lookups[0].State)
		})
	}
}

// The attributes that only the enrichment of the flag set gives are fetched in the same batch as the others: a bulk
// request makes one call, not one for each rule.
func TestPrefetchReadsTheEnrichedAttributes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		enrich    map[string]any
		request   map[string]any
		name      string
		wantCalls int
		wantValue bool
	}{
		{
			name:      "only the enrichment",
			enrich:    map[string]any{tenantAttribute: memberAlice},
			wantCalls: 1,
			wantValue: true,
		},
		{
			name: "the enrichment wins", enrich: map[string]any{tenantAttribute: memberBob},
			request: map[string]any{tenantAttribute: memberAlice}, wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			noCache := true
			env := &flagruntime.SegmentEnv{
				Binder: &memBinder{source: source}, Resolver: &bootstrap.SegmentResolver{
					Default: bootstrap.SegmentTuning{NoCache: &noCache},
				},
			}
			set := buildAndCommit(t, viewBuilderWith(t, env), viewContent(t, lookupDoc)).Runtime.FlagSets["a"]
			eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
				EvaluationContextEnrichment: tt.enrich,
			})

			evalCtx := ffcontext.NewEvaluationContextBuilder("k")
			for name, value := range tt.request {
				evalCtx.AddCustom(name, value)
			}

			states := eval.AllFlagsState(set, evalCtx.Build())

			assert.Equal(t, tt.wantValue, states.GetFlags()["tenant-twin"].Value)
			assert.Equal(t, tt.wantValue, states.GetFlags()["tenant-flag"].Value)
			assert.Equal(t, tt.wantCalls, source.Calls(segment.OpContains))
		})
	}
}

// The snapshot mode is refused whether or not the connections of the bootstrap file are known.
func TestSnapshotIsRefusedWithoutKnownConnections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		doc            string
		wantRejections int
		wantServed     bool
	}{
		{name: "snapshot", doc: audienceDoc(connAudiences, ""), wantRejections: 1},
		{name: "lookup", doc: audienceDoc(connAudiences, "lookup"), wantServed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{
				Segments: &flagruntime.SegmentEnv{Binder: &memBinder{source: source}},
			})
			built := buildAndCommit(t, builder, viewContent(t, tt.doc))

			assert.Len(t, built.SegmentRejections, tt.wantRejections)
			assert.Equal(t, tt.wantServed, built.Runtime.FlagSets["a"].Segments.View != nil)
		})
	}
}
