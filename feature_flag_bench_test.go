//go:build bench

package ffclient_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	sdkDefault = "sdkDefault"
)

// newFixtureClient starts a client on the benchmark fixture, closed when the benchmark ends. It fails the
// benchmark on error so that it never runs against an empty flag set.
func newFixtureClient(b *testing.B) *ffclient.GoFeatureFlag {
	b.Helper()

	client, err := ffclient.New(ffclient.Config{
		PollingInterval: 1 * time.Second,
		Retriever:       &fileretriever.Retriever{Path: "testdata/benchmark/flag-config.yaml"},
	})
	require.NoError(b, err)
	b.Cleanup(client.Close)
	b.ResetTimer()

	return client
}

func BenchmarkAllFlags(b *testing.B) {
	client := newFixtureClient(b)
	state := client.AllFlagsState(ffcontext.NewEvaluationContext("user-0"))
	require.True(b, state.IsValid(), "expected a valid flag state")
	require.Len(b, state.GetFlags(), 42, "expected 42 flags")
	b.ResetTimer()

	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))
		_ = client.AllFlagsState(user)
	}
}

func BenchmarkBoolVar_NoRule100(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.BoolVariation("bool-no-rule-100", user, false)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkBoolVar_NoRule0(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.BoolVariation("bool-no-rule-0", user, false)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkBoolVar_NoRule50(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.BoolVariation("bool-no-rule-50", user, false)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkBoolVar_Rule(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.BoolVariation("bool-rule", user, false)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkBoolVar_RuleComplex(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.BoolVariation("bool-rule-complex", user, false)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkBoolVar_RolloutProgressive(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.BoolVariation("bool-rollout-progressive", user, false)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkBoolVar_RolloutScheduled(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.BoolVariation("bool-rollout-scheduled", user, false)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkStringVar_NoRule100(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.StringVariation("string-no-rule-100", user, fixtures.Error)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkStringVar_NoRule0(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.StringVariation("string-no-rule-0", user, fixtures.Error)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkStringVar_NoRule50(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.StringVariation("string-no-rule-50", user, fixtures.Error)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkStringVar_Rule(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.StringVariation("string-rule", user, fixtures.Error)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkStringVar_RuleComplex(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.StringVariation("string-rule-complex", user, fixtures.Error)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkStringVar_RolloutProgressive(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.StringVariation("string-rollout-progressive", user, fixtures.Error)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkStringVar_RolloutScheduled(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.StringVariation("string-rollout-scheduled", user, fixtures.Error)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkIntVar_NoRule100(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.IntVariation("int-no-rule-100", user, 4)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkIntVar_NoRule0(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.IntVariation("int-no-rule-0", user, 4)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkIntVar_NoRule50(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.IntVariation("int-no-rule-50", user, 4)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkIntVar_Rule(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.IntVariation("int-rule", user, 4)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkIntVar_RuleComplex(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.IntVariation("int-rule-complex", user, 4)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkIntVar_RolloutProgressive(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.IntVariation("int-rollout-progressive", user, 4)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkIntVar_RolloutScheduled(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.IntVariation("int-rollout-scheduled", user, 4)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkFloat64Var_NoRule100(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.Float64Variation("float64-no-rule-100", user, 4.0)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkFloat64Var_NoRule0(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.Float64Variation("float64-no-rule-0", user, 4.0)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkFloat64Var_NoRule50(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.Float64Variation("float64-no-rule-50", user, 4.0)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkFloat64Var_Rule(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.Float64Variation("float64-rule", user, 4.0)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkFloat64Var_RuleComplex(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.Float64Variation("float64-rule-complex", user, 4.0)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkFloat64Var_RolloutProgressive(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.Float64Variation("float64-rollout-progressive", user, 4.0)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkFloat64Var_RolloutScheduled(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.Float64Variation("float64-rollout-scheduled", user, 4.0)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONVar_NoRule100(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONVariation(
			"json-no-rule-100",
			user,
			map[string]any{sdkDefault: fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONVar_NoRule0(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONVariation("json-no-rule-0", user, map[string]any{sdkDefault: fixtures.FlagsetDefault})
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONVar_NoRule50(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONVariation(
			"json-no-rule-50",
			user,
			map[string]any{sdkDefault: fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONVar_Rule(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONVariation("json-rule", user, map[string]any{sdkDefault: fixtures.FlagsetDefault})
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONVar_RuleComplex(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONVariation(
			"json-rule-complex",
			user,
			map[string]any{sdkDefault: fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONVar_RolloutProgressive(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONVariation(
			"json-rollout-progressive",
			user,
			map[string]any{sdkDefault: fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONVar_RolloutScheduled(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONVariation(
			"json-rollout-scheduled",
			user,
			map[string]any{sdkDefault: fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONArrayVar_NoRule100(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONArrayVariation(
			"jsonArr-no-rule-100",
			user,
			[]any{sdkDefault, fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONArrayVar_NoRule0(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONArrayVariation("jsonArr-no-rule-0", user, []any{sdkDefault, fixtures.FlagsetDefault})
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONArrayVar_NoRule50(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONArrayVariation("jsonArr-no-rule-50", user, []any{sdkDefault, fixtures.FlagsetDefault})
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONArrayVar_Rule(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONArrayVariation("jsonArr-rule", user, []any{sdkDefault, fixtures.FlagsetDefault})
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONArrayVar_RuleComplex(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONArrayVariation(
			"jsonArr-rule-complex",
			user,
			[]any{sdkDefault, fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONArrayVar_RolloutProgressive(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONArrayVariation(
			"jsonArr-rollout-progressive",
			user,
			[]any{sdkDefault, fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

func BenchmarkJSONArrayVar_RolloutScheduled(b *testing.B) {
	client := newFixtureClient(b)
	for i := range b.N {
		user := ffcontext.NewEvaluationContext(fmt.Sprintf(fixtures.UserKeyFormat, i))

		_, err := client.JSONArrayVariation(
			"jsonArr-rollout-scheduled",
			user,
			[]any{sdkDefault, fixtures.FlagsetDefault},
		)
		if err != nil {
			require.NoError(b, err)
		}
	}
}

/* Benchmark list:

Generate a dynamic flag file in the init method
for all tests.

- boolvariation classic
- boolvariation schedule
- boolvariation progressive
- boolvariation experimentation
- boolvariation 0%
- boolvariation 50%

- idem for all type of variations
- all flag with a lot of flags and different types

*/
