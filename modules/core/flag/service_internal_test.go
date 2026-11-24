package flag

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
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

func countEntries(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool {
		n++

		return true
	})

	return n
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
			f := newFlagWithQueries(tt.query)
			require.NoError(t, f.IsValid(first))
			ctx := ffcontext.NewEvaluationContextBuilder("user").AddCustom("plan", "pro").Build()
			_, err := (*f.Rules)[0].Evaluate(first, "user", ctx, "flag", false)
			require.NoError(t, err)

			assert.Equal(t, tt.wantFirstEntries, countEntries(&first.nikunjy.entries), "entries of the first Service")
			assert.Equal(t, tt.wantOtherEntries, countEntries(&other.nikunjy.entries), "entries of the other Service")

			first.Close()
			assert.Equal(t, tt.wantAfterClose, countEntries(&first.nikunjy.entries), "entries after Close")
			// still usable after Close: it rebuilds what it needs
			_, err = (*f.Rules)[0].Evaluate(first, "user", ctx, "flag", false)
			require.NoError(t, err)
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
			f := &InternalFlag{
				Variations:      &map[string]*any{"on": new(any(true))},
				DefaultRule:     &Rule{VariationResult: new("on")},
				Experimentation: &ExperimentationRollout{Start: new(clocktest.Instant().Add(-time.Hour))},
			}
			ctx := ffcontext.NewEvaluationContext("user")
			ctx.AddCustomAttribute(
				tt.attribute,
				map[string]any{"currentDateTime": clocktest.Instant().Format(time.RFC3339)},
			)

			for range tt.evaluations {
				_, details := f.Value("flag", ctx, Context{Service: svc})
				require.Empty(t, details.ErrorCode)
			}

			assert.Equal(t, tt.wantWarnings, strings.Count(buf.String(), "legacy name"))
			// another Service has its own warn-once state
			other := NewService(WithLogger(slog.New(slog.NewTextHandler(&buf, nil))))
			_, _ = f.Value("flag", ctx, Context{Service: other})
			assert.Equal(t, 2*tt.wantWarnings, strings.Count(buf.String(), "legacy name"))
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
			_, err := r.Evaluate(svc, "user", ffcontext.NewEvaluationContext("user"), "flag", false)
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
			cleanup: func(c *nikunjyEvaluatorCache, epoch uint64) {
				var keys []uint64
				c.byKey.Range(func(k, _ any) bool {
					keys = append(keys, k.(uint64))

					return true
				})
				c.unindexAt(epoch, keys)
			},
			count: func(c *nikunjyEvaluatorCache) int { return countEntries(&c.byKey) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newIndexingService()
			c := svc.nikunjy
			oldEpoch := c.epoch.Load()
			first := newFlagWithQueries(query)
			require.NoError(t, first.IsValid(svc))
			svc.Close()
			rebuilt := newFlagWithQueries(query)
			require.NoError(t, rebuilt.IsValid(svc))
			require.Equal(t, 1, tt.count(c), "the rebuilt rule list is cached")

			tt.cleanup(c, oldEpoch)
			assert.Equal(
				t,
				tt.wantAfterEarlierEpoch,
				tt.count(c),
				"the cleanup of the rule list of the earlier epoch must not evict anything",
			)

			tt.cleanup(c, c.epoch.Load())
			assert.Equal(t, tt.wantAfterCurrentEpoch, tt.count(c), "the cleanup of the current epoch does release")
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
			f := &InternalFlag{
				Variations: &map[string]*any{"a": new(any(true)), "b": new(any(false))},
				DefaultRule: &Rule{ProgressiveRollout: &ProgressiveRollout{
					Initial: &ProgressiveRolloutStep{Variation: new("a"), Percentage: new(float64(0)), Date: &start},
					End:     &ProgressiveRolloutStep{Variation: new("b"), Percentage: new(float64(100)), Date: &end},
				}},
			}
			ctx := ffcontext.NewEvaluationContext("user")
			ctx.AddCustomAttribute(ffcontext.LegacyReservedKey, map[string]any{
				"currentDateTime": clocktest.Instant().Format(time.RFC3339),
			})

			for range tt.evaluations {
				_, details := f.Value("flag", ctx, Context{Service: svc})
				require.Empty(t, details.ErrorCode)
			}

			assert.Equal(t, tt.wantWarnings, strings.Count(buf.String(), "legacy name"))
		})
	}
}
