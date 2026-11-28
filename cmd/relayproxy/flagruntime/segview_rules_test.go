package flagruntime_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	tenantAttribute = "tenant"
	audienceFlag    = "audience-flag"
)

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
			name: "a minute and ten minutes",
			doc:  lookupDoc + "    version: {key: v1, interval: 1m}\n" + sprintOther(", interval: 10m"),
			at:   []time.Duration{0, 6 * time.Second, 11 * time.Second}, wantCalls: 2,
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

			states := eval.AllFlagsState(t.Context(), set, evalCtx.Build())

			assert.Equal(t, tt.wantValue, states.GetFlags()["tenant-twin"].Value)
			assert.Equal(t, tt.wantValue, states.GetFlags()["tenant-flag"].Value)
			assert.Equal(t, tt.wantCalls, source.Calls(segment.OpContains))
		})
	}
}

// The snapshot mode is served like the lookup mode when the connections of the bootstrap file are not known.
func TestSnapshotIsServedWithoutKnownConnections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		doc            string
		wantRejections int
		wantServed     bool
	}{
		{name: "snapshot", doc: audienceDoc(connAudiences, ""), wantServed: true},
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

// A flag whose segment is not served has the same status and cacheability on every endpoint, whatever the other flags
// of the set use: NOT_CONFIGURED, not cacheable, on a single evaluation and in a bulk one.
func TestUnservedSegmentHasOneStatusOnEveryEndpoint(t *testing.T) {
	t.Parallel()

	const doc = lookupDoc + "  - name: ghost\n    connection: nowhere\n    mode: lookup\n"

	tests := []struct {
		name       string
		flag       string
		wantStatus segment.Status
	}{
		{name: "rejected", flag: "ghost-flag", wantStatus: segment.StatusNotConfigured},
		{name: "served", flag: audienceFlag, wantStatus: segment.StatusHealthy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			set := viewSet(t, &memBinder{source: source}, doc)
			eval := flagruntime.NewEvaluator(newService(t), nil)
			evalCtx := ffcontext.NewEvaluationContext(memberAlice)

			single, err := eval.BoolVariationDetails(t.Context(), set, tt.flag, evalCtx, false)
			require.NoError(t, err)

			all := eval.AllFlagsState(t.Context(), set, evalCtx)
			one := eval.GetFlagStates(t.Context(), set, evalCtx, []string{tt.flag})
			bulk, listed := all.GetFlags()[tt.flag], one.GetFlags()[tt.flag]

			assert.Equal(t, tt.wantStatus, single.SegmentStatus, "single")
			assert.Equal(t, tt.wantStatus, bulk.SegmentStatus, "bulk")
			assert.Equal(t, tt.wantStatus, listed.SegmentStatus, "bulk of listed flags")

			_, bulkCacheable := bulk.Metadata[flagruntime.MetadataCacheable]

			assert.Equal(t, single.Cacheable, bulkCacheable)
		})
	}
}

// countingHandler counts the records it gets.
type countingHandler struct{ records *atomic.Int64 }

func (countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h countingHandler) Handle(context.Context, slog.Record) error {
	h.records.Add(1)

	return nil
}

func (h countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h countingHandler) WithGroup(string) slog.Handler { return h }

// A store that is down does not write a line for every evaluation: the evaluations answer STORE_ERROR and the
// outage is told by the log of the connection (once, when it goes down) and by the metrics.
func TestAnOutageDoesNotLogForEveryEvaluation(t *testing.T) {
	t.Parallel()

	const evaluations = 1_000

	tests := []struct {
		ctx  ffcontext.Context
		name string
		flag string
	}{
		{name: "read by the batch", flag: audienceFlag, ctx: ffcontext.NewEvaluationContext(memberAlice)},
		{
			name: "read by the rule", flag: "typed-flag",
			ctx: ffcontext.NewEvaluationContextBuilder("k").AddCustom("user", struct {
				ID string `json:"id"`
			}{ID: memberAlice}).Build(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var records atomic.Int64

			svc := flagdef.NewService(flagdef.WithLogger(slog.New(countingHandler{records: &records})))
			t.Cleanup(svc.Close)

			source := segment.NewMemorySource(map[string][]string{audienceSegmentName: {memberAlice}})
			source.Fail(segment.OpContains, errStoreDown)

			set := viewSet(t, &memBinder{source: source}, lookupDoc)
			eval := flagruntime.NewEvaluator(svc, nil)

			for range evaluations {
				got, err := eval.BoolVariationDetails(t.Context(), set, tt.flag, tt.ctx, false)
				require.NoError(t, err)
				require.Equal(t, segment.StatusStoreError, got.SegmentStatus)
			}

			assert.Zero(t, records.Load())
		})
	}
}
