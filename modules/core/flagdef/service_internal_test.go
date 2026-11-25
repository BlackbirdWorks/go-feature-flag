package flagdef

import (
	"bytes"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	legacyName = "legacy name"
)

func countEntries(entries *sync.Map) int {
	count := 0

	entries.Range(func(_, _ any) bool {
		count++

		return true
	})

	return count
}

func TestService_StateIsOwnedByTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// evaluate is what the first Service evaluates; the second one never does.
		query            string
		wantFirstEntries int
		wantOtherEntries int
		wantAfterClose   int
	}{
		{name: "a nikunjy query", query: `plan eq "pro"`, wantFirstEntries: 1, wantOtherEntries: 0, wantAfterClose: 0},
		{
			name:             "a short json logic query",
			query:            `{"==": [{"var": "plan"}, "pro"]}`,
			wantFirstEntries: 0,
			wantOtherEntries: 0,
			wantAfterClose:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			first, other := NewService(), NewService()
			flg := newFlagWithQueries(tt.query)
			require.NoError(t, flg.IsValid(first))

			ctx := ffcontext.NewEvaluationContextBuilder(fixtures.ContextKindUser).
				AddCustom(fixtures.AttrPlan, fixtures.PlanPro).
				Build()
			got, err := (*flg.Rules)[0].Evaluate(first, fixtures.ContextKindUser, ctx, fixtures.FlagKeyBare, false)
			require.NoError(t, err)
			assert.Equal(t, "on", got)

			assert.Equal(t, tt.wantFirstEntries, countEntries(&first.nikunjy.entries), "entries of the first Service")
			assert.Equal(t, tt.wantOtherEntries, countEntries(&other.nikunjy.entries), "entries of the other Service")

			first.Close()
			assert.Equal(t, tt.wantAfterClose, countEntries(&first.nikunjy.entries), "entries after Close")
			// still usable after Close: it rebuilds what it needs
			got2, err := (*flg.Rules)[0].Evaluate(first, fixtures.ContextKindUser, ctx, fixtures.FlagKeyBare, false)
			require.NoError(t, err)
			assert.Equal(t, "on", got2)
		})
	}
}

// Observable outcome: Close on a nil *Service returns without panicking.
func TestService_CloseOfNilServiceIsANoop(t *testing.T) {
	t.Parallel()

	var svc *Service
	assert.NotPanics(t, svc.Close)
}

func TestService_LegacyAttributeIsReportedOncePerService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		attribute    string
		evaluations  int
		wantWarnings int
	}{
		{
			name:         "legacy attribute, several evaluations",
			evaluations:  3,
			attribute:    ffcontext.LegacyReservedKey,
			wantWarnings: 1,
		},
		{name: "current attribute", evaluations: 3, attribute: ffcontext.ReservedKey, wantWarnings: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			svc := NewService(WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			flg := &InternalFlag{
				Variations:      &map[string]*any{"on": new(any(true))},
				DefaultRule:     &Rule{VariationResult: new("on")},
				Experimentation: &ExperimentationRollout{Start: new(clocktest.Instant().Add(-time.Hour))},
			}
			ctx := ffcontext.NewEvaluationContext(fixtures.ContextKindUser)
			ctx.AddCustomAttribute(
				tt.attribute,
				map[string]any{fixtures.CurrentDateTime: clocktest.Instant().Format(time.RFC3339)},
			)

			for range tt.evaluations {
				_, details := flg.Value(fixtures.FlagKeyBare, ctx, Context{Service: svc})
				require.Empty(t, details.ErrorCode)
			}

			assert.Equal(t, tt.wantWarnings, strings.Count(buf.String(), legacyName))
			// another Service has its own warn-once state
			other := NewService(WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			_, _ = flg.Value(fixtures.FlagKeyBare, ctx, Context{Service: other})

			assert.Equal(t, 2*tt.wantWarnings, strings.Count(buf.String(), legacyName))
		})
	}
}

func TestService_LoggerReceivesTheEvaluationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		logger     *slog.Logger
		name       string
		wantLogged bool
	}{
		{name: "with a logger", logger: nil, wantLogged: true},
		{name: "without a logger nothing is logged", logger: slog.New(slog.DiscardHandler), wantLogged: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			opt := WithLogger(slog.New(slog.NewTextHandler(&buf, nil)))
			if tt.logger != nil {
				opt = WithLogger(tt.logger)
			}

			svc := NewService(opt)
			// a nikunjy query that does not parse is logged by the evaluation
			r := Rule{Query: new(`plan eq`), VariationResult: new("on")}
			_, err := r.Evaluate(
				svc,
				fixtures.ContextKindUser,
				ffcontext.NewEvaluationContext(fixtures.ContextKindUser),
				fixtures.FlagKeyBare,
				false,
			)
			require.Error(t, err)

			assert.Equal(t, tt.wantLogged, buf.Len() > 0)
		})
	}
}

func TestNikunjyEvaluatorCache_ResetIgnoresTheCleanupsOfEarlierEpochs(t *testing.T) {
	t.Parallel()

	const query = `plan eq "kept-after-close"`

	tests := []struct {
		// cleanup runs the cleanup of the rule list validated before the reset, for the epoch given.
		cleanup func(c *nikunjyEvaluatorCache, epoch uint64)
		// count is what the cleanup would remove: the evaluator entries or the keys.
		count func(c *nikunjyEvaluatorCache) int
		name  string
		// wantAfterEarlierEpoch and wantAfterCurrentEpoch are the count after the cleanup of the earlier and of
		// the current epoch
		wantAfterEarlierEpoch, wantAfterCurrentEpoch int
	}{
		{
			name:                  "the queries of the evaluator cache",
			wantAfterEarlierEpoch: 1, wantAfterCurrentEpoch: 0,
			cleanup: func(c *nikunjyEvaluatorCache, epoch uint64) { c.releaseAt(epoch, []string{query}) },
			count:   func(c *nikunjyEvaluatorCache) int { return countEntries(&c.entries) },
		},
		{
			name:                  "the keys of the index",
			wantAfterEarlierEpoch: 1, wantAfterCurrentEpoch: 0,
			cleanup: func(evalCache *nikunjyEvaluatorCache, epoch uint64) {
				var keys []uint64

				evalCache.byKey.Range(func(k, _ any) bool {
					if key, ok := k.(uint64); ok {
						keys = append(keys, key)
					}

					return true
				})
				evalCache.unindexAt(epoch, keys)
			},
			count: func(c *nikunjyEvaluatorCache) int { return countEntries(&c.byKey) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			evalCache := svc.nikunjy
			oldEpoch := evalCache.epoch.Load()
			first := newFlagWithQueries(query)
			require.NoError(t, first.IsValid(svc))
			svc.Close()

			rebuilt := newFlagWithQueries(query)
			require.NoError(t, rebuilt.IsValid(svc))
			require.Equal(t, 1, tt.count(evalCache), "the rebuilt rule list is cached")

			tt.cleanup(evalCache, oldEpoch)
			assert.Equal(
				t,
				tt.wantAfterEarlierEpoch,
				tt.count(evalCache),
				"the cleanup of the rule list of the earlier epoch must not evict anything",
			)

			tt.cleanup(evalCache, evalCache.epoch.Load())
			assert.Equal(
				t,
				tt.wantAfterCurrentEpoch,
				tt.count(evalCache),
				"the cleanup of the current epoch does release",
			)
			runtime.KeepAlive(first)
			runtime.KeepAlive(rebuilt)
		})
	}
}

func TestService_ProgressiveRolloutReportsTheLegacyAttributeOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		evaluations  int
		wantWarnings int
	}{
		{name: "one evaluation", evaluations: 1, wantWarnings: 1},
		{name: "many evaluations", evaluations: 5, wantWarnings: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			svc := NewService(WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			start, end := clocktest.Instant().Add(-time.Hour), clocktest.Instant().Add(time.Hour)
			flg := &InternalFlag{
				Variations: &map[string]*any{"a": new(any(true)), "b": new(any(false))},
				DefaultRule: &Rule{ProgressiveRollout: &ProgressiveRollout{
					Initial: &ProgressiveRolloutStep{Variation: new("a"), Percentage: new(float64(0)), Date: &start},
					End:     &ProgressiveRolloutStep{Variation: new("b"), Percentage: new(float64(100)), Date: &end},
				}},
			}
			ctx := ffcontext.NewEvaluationContext(fixtures.ContextKindUser)
			ctx.AddCustomAttribute(ffcontext.LegacyReservedKey, map[string]any{
				fixtures.CurrentDateTime: clocktest.Instant().Format(time.RFC3339),
			})

			for range tt.evaluations {
				_, details := flg.Value(fixtures.FlagKeyBare, ctx, Context{Service: svc})
				require.Empty(t, details.ErrorCode)
			}

			assert.Equal(t, tt.wantWarnings, strings.Count(buf.String(), legacyName))
		})
	}
}
