package flagruntime_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
)

// equivalenceFile is one flag file of the equivalence table: a path in the repository, or inline bytes.
type equivalenceFile struct {
	name   string
	path   string
	inline string
	// minFlags is the number of valid flags the file has: the table must not shrink under it.
	minFlags int
}

func equivalenceFiles() []equivalenceFile {
	repo := []equivalenceFile{
		{path: "testdata/flag-config.yaml", minFlags: 2},
		{path: "testdata/flag-config.json", minFlags: 2},
		{path: "testdata/flag-config.toml", minFlags: 2},
		{path: "testdata/flag-config-rollouts.toml", minFlags: 1},
		{path: "testdata/flag-config-scheduled-v0.yaml", minFlags: 2},
		{path: "testdata/flag-config-custom-bucketingkey.yaml", minFlags: 1},
		{path: "testdata/flag-config-multiline-query.json", minFlags: 1},
		{path: "testdata/flag-config-updated.yaml", minFlags: 2},
		{path: "testdata/flag-config-2nd-file.yaml", minFlags: 2},
		{path: "testdata/flag-config-3rd-file.yaml", minFlags: 7},
		{path: "testdata/invalid-flag-config.yaml", minFlags: 0}, // every flag of it is rejected, as in version 1
		{path: "testdata/benchmark/flag-config.yaml", minFlags: 42},
		{path: "openfeature/provider_tests/flags.yaml", minFlags: 13},
		{path: "testdata/ffclient/all_flags/config_flag/flag-config-all-flags.yaml", minFlags: 7},
		{path: "testdata/ffclient/all_flags/config_flag/flag-config-with-error.yaml", minFlags: 6},
		{path: "cmdhelpers/configfile/testdata/flag.goff.yaml", minFlags: 2},
	}

	files := make([]equivalenceFile, 0, len(repo)+2)

	for _, file := range repo {
		file.name, file.path = file.path, repoRoot+file.path
		files = append(files, file)
	}

	return append(files,
		equivalenceFile{name: "shapes", path: shapesFile, minFlags: 16},
		equivalenceFile{name: "scenarios", inline: loadtest.FlagsYAML(), minFlags: 48},
	)
}

// materialise returns the path and the bytes of a file of the table (an inline one is written to a temp dir).
func (f equivalenceFile) materialise(tb testing.TB) (string, []byte) {
	tb.Helper()

	if f.inline == "" {
		return f.path, readFile(tb, f.path)
	}

	path := filepath.Join(tb.TempDir(), "flags.yaml")
	require.NoError(tb, writeFile(path, []byte(f.inline)))

	return path, []byte(f.inline)
}

// errText is the text of an error, empty for nil: the results must agree on it.
func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// mustJSON is the JSON encoding of a value; the evaluation results are compared on it too.
func mustJSON(tb testing.TB, val any) string {
	tb.Helper()

	data, err := json.Marshal(val)
	require.NoError(tb, err)

	return string(data)
}

// The result of every evaluation call of the runtime is the one of the version 1 client for the same flag file,
// field by field: value, variation, reason, error code and details, metadata, cacheable, version, trackEvents and
// failed; and the error. A mismatch would mean that a client sees another answer after the switch.
func TestEquivalence_SingleEvaluation(t *testing.T) {
	t.Parallel()

	ctxs := equivalenceContexts()

	var pairs atomic.Int64

	// the parent ends after its parallel subtests: the table as a whole must keep its size
	t.Cleanup(func() { assert.GreaterOrEqual(t, pairs.Load(), int64(minPairs), "flag and context pairs evaluated") })

	for _, file := range equivalenceFiles() {
		t.Run(file.name, func(t *testing.T) {
			t.Parallel()

			path, data := file.materialise(t)
			legacy := versionOne(t, path, nil, "", nil)
			eval := flagruntime.NewEvaluator(newService(t), nil)
			set := runtimeOf(t, data, formatOf(path))
			names := flagNamesOf(t, data, formatOf(path))

			// the flags of the file that the engine accepted: a file that parsed to nothing would pass with no check
			assert.GreaterOrEqual(t, len(set.Flags), file.minFlags, "valid flags of %s", file.name)

			for _, name := range names {
				for _, ctx := range ctxs {
					compareSingle(t, legacy, eval, set, name, ctx)
					pairs.Add(1)
				}
			}
		})
	}
}

// compareSingle evaluates one flag with every typed call and the raw call, through both, and compares.
func compareSingle(
	t *testing.T, legacy *ffclient.GoFeatureFlag, eval *flagruntime.Evaluator, set *flagruntime.FlagSet,
	name string, ctx ffcontext.Context,
) {
	t.Helper()

	label := fmt.Sprintf("%s/%s", name, keyOf(ctx))

	for _, def := range []any{nil, "sdk-default", 42, map[string]any{"d": 1}} {
		want, wantErr := legacy.RawVariation(name, ctx, def)
		got, gotErr := eval.RawVariation(set, name, ctx, def)
		assert.Equal(t, want, got, "raw %s", label)
		assert.Equal(t, errText(wantErr), errText(gotErr), "raw error %s", label)
	}

	wantB, wantBErr := legacy.BoolVariationDetails(name, ctx, true)
	gotB, gotBErr := eval.BoolVariationDetails(set, name, ctx, true)
	assert.Equal(t, wantB, gotB, "bool %s", label)
	assert.Equal(t, errText(wantBErr), errText(gotBErr), "bool error %s", label)

	wantI, wantIErr := legacy.IntVariationDetails(name, ctx, 7)
	gotI, gotIErr := eval.IntVariationDetails(set, name, ctx, 7)
	assert.Equal(t, wantI, gotI, "int %s", label)
	assert.Equal(t, errText(wantIErr), errText(gotIErr), "int error %s", label)

	wantF, wantFErr := legacy.Float64VariationDetails(name, ctx, 0.5)
	gotF, gotFErr := eval.Float64VariationDetails(set, name, ctx, 0.5)
	assert.Equal(t, wantF, gotF, "float %s", label)
	assert.Equal(t, errText(wantFErr), errText(gotFErr), "float error %s", label)

	wantS, wantSErr := legacy.StringVariationDetails(name, ctx, "s")
	gotS, gotSErr := eval.StringVariationDetails(set, name, ctx, "s")
	assert.Equal(t, wantS, gotS, "string %s", label)
	assert.Equal(t, errText(wantSErr), errText(gotSErr), "string error %s", label)

	wantA, wantAErr := legacy.JSONArrayVariationDetails(name, ctx, []any{"d"})
	gotA, gotAErr := eval.JSONArrayVariationDetails(set, name, ctx, []any{"d"})
	assert.Equal(t, wantA, gotA, "array %s", label)
	assert.Equal(t, errText(wantAErr), errText(gotAErr), "array error %s", label)

	wantO, wantOErr := legacy.JSONVariationDetails(name, ctx, map[string]any{"d": 1})
	gotO, gotOErr := eval.JSONVariationDetails(set, name, ctx, map[string]any{"d": 1})
	assert.Equal(t, wantO, gotO, "object %s", label)
	assert.Equal(t, errText(wantOErr), errText(gotOErr), "object error %s", label)
}

// All flags and the listed flags: the JSON the endpoints write is byte for byte the one of the version 1 client.
func TestEquivalence_FlagStates(t *testing.T) {
	t.Parallel()

	ctxs := equivalenceContexts()

	for _, file := range equivalenceFiles() {
		t.Run(file.name, func(t *testing.T) {
			t.Parallel()

			path, data := file.materialise(t)
			legacy := versionOne(t, path, nil, "", nil)
			eval := flagruntime.NewEvaluator(newService(t), nil)
			set := runtimeOf(t, data, formatOf(path))
			names := flagNamesOf(t, data, formatOf(path))

			subset := []string{missingKey}
			if len(names) > 2 {
				subset = append(subset, names[0], names[len(names)/2])
			}

			for _, ctx := range ctxs {
				want := legacy.AllFlagsState(ctx)
				got := eval.AllFlagsState(set, ctx)

				assert.Equal(t, mustJSON(t, want), mustJSON(t, got), "all flags of %s", keyOf(ctx))
				assert.Equal(t, want.GetFlags(), got.GetFlags(), "the states of %s, Failed included", keyOf(ctx))
				assert.Equal(t, want.IsValid(), got.IsValid(), "validity of %s", keyOf(ctx))

				wantSubset := legacy.GetFlagStates(ctx, subset)
				gotSubset := eval.GetFlagStates(set, ctx, subset)
				assert.Equal(t, mustJSON(t, wantSubset), mustJSON(t, gotSubset), "listed flags of %s", keyOf(ctx))
				assert.Equal(t, wantSubset.GetFlags(), gotSubset.GetFlags(), "listed states of %s", keyOf(ctx))
				assert.Equal(t, wantSubset.IsValid(), gotSubset.IsValid())
			}
		})
	}
}

// The configuration view and the hash of /v1/flag/change are those of the version 1 handlers, which marshal
// the deep copies of the flags of the cache.
func TestEquivalence_ConfigurationAndChange(t *testing.T) {
	t.Parallel()

	for _, file := range equivalenceFiles() {
		t.Run(file.name, func(t *testing.T) {
			t.Parallel()

			path, data := file.materialise(t)
			legacy := versionOne(t, path, nil, "", nil)
			set := runtimeOf(t, data, formatOf(path))
			names := flagNamesOf(t, data, formatOf(path))

			wantFlags, err := legacy.GetFlagsFromCache()
			require.NoError(t, err)

			assert.JSONEq(t, mustJSON(t, wantFlags), mustJSON(t, set.Configuration(nil)))

			filter := []string{names[0], missingKey}
			wantFiltered := map[string]any{}

			for _, key := range filter {
				if flag, ok := wantFlags[key]; ok {
					wantFiltered[key] = flag
				}
			}

			assert.JSONEq(t, mustJSON(t, wantFiltered), mustJSON(t, set.Configuration(filter)))

			wantHashes := map[string]uint32{}

			for key, flag := range wantFlags {
				wantHashes[key] = evalcore.Hash(mustJSON(t, flag))
			}

			change, err := set.Change()
			require.NoError(t, err)
			assert.Equal(t, evalcore.Hash(mustJSON(t, wantFlags)), change.Hash)
			assert.Equal(t, wantHashes, change.Flags)
		})
	}
}

// The enrichment of the configuration, the environment and the per-flag-set enrichment evaluate as the
// enrichment and the environment of the version 1 client do.
func TestEquivalence_EnrichmentAndEnvironment(t *testing.T) {
	t.Parallel()

	path, data := equivalenceFile{path: shapesFile}.materialise(t)
	set := runtimeOf(t, data, formatOf(path))
	enrichment := map[string]any{planAttr: proPlan, "country": "FR", "region": "eu"}

	// every row says what it must show: the enrichment (the plan of the context is "pro" without the context saying
	// so) is applied in both rows, the environment only in the second, and the result of each flag is asserted
	tests := []struct {
		name         string
		environment  string
		wantEnvFlag  bool
		wantPlanFlag float64
	}{
		{name: "enrichment only", wantEnvFlag: false, wantPlanFlag: 2.25},
		{name: "enrichment and environment", environment: prodEnv, wantEnvFlag: true, wantPlanFlag: 2.25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			legacy := versionOne(t, path, enrichment, tt.environment, nil)
			eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
				EvaluationContextEnrichment: enrichment, Environment: tt.environment,
			})

			for _, name := range flagNamesOf(t, data, formatOf(path)) {
				for _, ctx := range equivalenceContexts() {
					want, wantErr := legacy.RawVariation(name, ctx, nil)
					got, gotErr := eval.RawVariation(set, name, ctx, nil)
					assert.Equal(t, want, got, "%s/%s", name, keyOf(ctx))
					assert.Equal(t, errText(wantErr), errText(gotErr))
				}
			}

			plain := ffcontext.NewEvaluationContext("plain-user")
			planFlag, _ := eval.RawVariation(set, "float-flag", plain, nil)
			envFlag, _ := eval.RawVariation(set, "env-flag", plain, nil)

			assert.InDelta(t, tt.wantPlanFlag, planFlag.Value, 0, "the enrichment is applied")
			assert.Equal(t, tt.wantEnvFlag, envFlag.Value, "the environment is applied only when it is set")

			assert.Equal(t, legacy.GetEvaluationContextEnrichment(), eval.EvaluationContextEnrichment(set))
			assert.Equal(t,
				mustJSON(t, legacy.AllFlagsState(ffcontext.NewEvaluationContext("u"))),
				mustJSON(t, eval.AllFlagsState(set, ffcontext.NewEvaluationContext("u"))))
		})
	}
}

// An attribute that the request and the enrichment both carry has the value of the enrichment, in version 1 and in
// the runtime alike.
func TestEquivalence_EnrichmentWinsOverTheRequest(t *testing.T) {
	t.Parallel()

	path, data := equivalenceFile{path: shapesFile}.materialise(t)
	set := runtimeOf(t, data, formatOf(path))
	enrichment := map[string]any{planAttr: proPlan}

	legacy := versionOne(t, path, enrichment, "", nil)
	eval := flagruntime.NewEvaluator(
		newService(t),
		&flagruntime.EvaluatorConfig{EvaluationContextEnrichment: enrichment},
	)

	ctx := ffcontext.NewEvaluationContext("user")
	ctx.AddCustomAttribute(planAttr, "free")

	want, wantErr := legacy.RawVariation("float-flag", ctx, nil)
	got, gotErr := eval.RawVariation(set, "float-flag", ctx, nil)

	require.NoError(t, wantErr)
	require.NoError(t, gotErr)
	assert.Equal(t, want, got)
	assert.InDelta(t, 2.25, got.Value, 0, "the value of the enrichment (pro) wins over the request (free)")
}

// Tracked evaluations produce the same events as the version 1 client: same flags, same fields.
func TestEquivalence_TrackedEvents(t *testing.T) {
	t.Parallel()

	path, data := equivalenceFile{path: shapesFile}.materialise(t)
	set := runtimeOf(t, data, formatOf(path))
	enrichment := map[string]any{"semaphore": map[string]any{"exporterMetadata": map[string]any{"region": "eu"}}}

	// every row says what the events must carry: the exporter metadata comes from the enrichment, or is absent
	tests := []struct {
		wantRegion any
		enrichment map[string]any
		name       string
	}{
		{name: "no enrichment", wantRegion: nil},
		{name: "enrichment that the events read", enrichment: enrichment, wantRegion: "eu"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sink := &collector{}
			legacy := versionOne(t, path, tt.enrichment, "", sink)

			var got []*exporter.FeatureEvent

			eval := flagruntime.NewEvaluator(newService(t), &flagruntime.EvaluatorConfig{
				EvaluationContextEnrichment: tt.enrichment,
				TrackEvent:                  func(event *exporter.FeatureEvent) { got = append(got, event) },
			})

			for _, name := range flagNamesOf(t, data, formatOf(path)) {
				for _, ctx := range equivalenceContexts() {
					if ctx == nil {
						continue // an event describes its context: version 1 and the runtime both need one
					}

					_, _ = legacy.RawVariation(name, ctx, nil)
					_, _ = eval.RawVariation(set, name, ctx, nil)
					_, _ = legacy.BoolVariationDetails(name, ctx, false)
					_, _ = eval.BoolVariationDetails(set, name, ctx, false)
				}
			}

			legacy.Close()

			want := sink.events

			require.NotEmpty(t, want, "the table tracks some evaluations")
			assert.Len(t, got, len(want))

			sortEvents(want)
			sortEvents(got)

			for idx := range min(len(want), len(got)) {
				assert.Equal(t, *want[idx], *got[idx], "event %d", idx)
				assert.Equal(t, tt.wantRegion, got[idx].Metadata["region"], "the exporter metadata of event %d", idx)
			}
		})
	}
}

func sortEvents(events []*exporter.FeatureEvent) {
	slices.SortStableFunc(events, func(a, b *exporter.FeatureEvent) int {
		return compareStrings(a.Key+"\x00"+a.UserKey+"\x00"+a.Variation+fmt.Sprint(a.Value),
			b.Key+"\x00"+b.UserKey+"\x00"+b.Variation+fmt.Sprint(b.Value))
	})
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
