//go:build bench

package ffclient_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func newBenchClient(b *testing.B, yaml string, withExporter bool) *ffclient.GoFeatureFlag {
	b.Helper()
	dir := b.TempDir()
	path := filepath.Join(dir, "flags.yaml")
	require.NoError(b, os.WriteFile(path, []byte(yaml), 0o600))

	cfg := ffclient.Config{
		PollingInterval: time.Hour,
		Retriever:       &fileretriever.Retriever{Path: path},
	}
	if withExporter {
		cfg.DataExporter = ffclient.DataExporter{
			FlushInterval:    time.Second,
			MaxEventInMemory: 10000,
			Exporter:         loadtest.NewExporter(loadtest.ExporterDiscard),
		}
	}

	c, err := ffclient.New(&cfg)
	require.NoError(b, err)

	return c
}

// getScenarioClient starts a client on the scenarios flags, closed when the benchmark ends.
func getScenarioClient(b *testing.B) *ffclient.GoFeatureFlag {
	b.Helper()
	c := newBenchClient(b, loadtest.FlagsYAML(), true)
	b.Cleanup(c.Close)

	return c
}

// benchContext builds a realistic evaluation context (about 10 attributes).
func benchContext(i int) ffcontext.EvaluationContext {
	return ffcontext.NewEvaluationContextBuilder(fmt.Sprintf(fixtures.UserKeyFormat, i%1000)).
		AddCustom("email", fmt.Sprintf("user-%d@example.com", i%1000)).
		AddCustom("country", "FR").
		AddCustom("age", 34).
		AddCustom(fixtures.Plan, "pro").
		AddCustom(regionName, "eu-west-1").
		AddCustom("beta", true).
		AddCustom("version", "1.42.0").
		AddCustom("device", "ios").
		AddCustom("company", "acme").
		Build()
}

func evalOne(client *ffclient.GoFeatureFlag, key, typ string, ctx ffcontext.Context) error {
	var err error

	switch typ {
	case "string":
		_, err = client.StringVariation(key, ctx, "x")
	case protocol.FormatJSON:
		_, err = client.JSONVariation(key, ctx, map[string]any{})
	default:
		_, err = client.BoolVariation(key, ctx, false)
	}

	return err
}

// BenchmarkScenarios evaluates every scenario/type with event tracking OFF and ON, sequentially.
func BenchmarkScenarios(b *testing.B) {
	client := getScenarioClient(b)

	for _, scenario := range loadtest.Scenarios() {
		for _, typ := range loadtest.Types() {
			for _, track := range []bool{false, true} {
				// to keep the matrix readable, only bool is run for every scenario;
				// string and json are run for the no-rule and the targeting scenarios.
				if typ != fixtures.BoolType && scenario != "norule" && scenario != "rule" {
					continue
				}

				key := loadtest.FlagKey(scenario, typ, track)
				name := fmt.Sprintf("%s/%s/track=%t", scenario, typ, track)
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()

					for i := range b.N {
						require.NoError(b, evalOne(client, key, typ, benchContext(i)))
					}
				})
			}
		}
	}
}

// BenchmarkScenariosParallel is the same matrix as BenchmarkScenarios using all cores.
func BenchmarkScenariosParallel(b *testing.B) {
	client := getScenarioClient(b)

	for _, sc := range loadtest.Scenarios() {
		for _, track := range []bool{false, true} {
			key := loadtest.FlagKey(sc, fixtures.BoolType, track)
			b.Run(fmt.Sprintf("%s/bool/track=%t", sc, track), func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					i := 0
					for pb.Next() {
						i++

						err := evalOne(client, key, fixtures.BoolType, benchContext(i))
						if err != nil {
							assert.NoError(b, err)

							return
						}
					}
				})
			})
		}
	}
}

// BenchmarkAllFlagsState measures AllFlagsState for 10, 100 and 1000 flags.
func BenchmarkAllFlagsState(b *testing.B) {
	for _, numFlags := range []int{10, 100, 1000} {
		client := newBenchClient(b, loadtest.ManyFlagsYAML(numFlags), false)
		b.Run(fmt.Sprintf("flags=%d", numFlags), func(b *testing.B) {
			b.ReportAllocs()

			for i := range b.N {
				s := client.AllFlagsState(benchContext(i))
				require.Len(b, s.GetFlags(), numFlags, "expected %d flags", numFlags)
			}
		})
		b.Run(fmt.Sprintf("flags=%d/parallel", numFlags), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					i++
					_ = client.AllFlagsState(benchContext(i))
				}
			})
		})
		client.Close()
	}
}

// BenchmarkScenariosPrebuiltContext measures the engine alone: the evaluation context (about 10
// attributes) is built once and shared, as a service that reuses its contexts would do.
func BenchmarkScenariosPrebuiltContext(b *testing.B) {
	client := getScenarioClient(b)
	ctx := benchContext(5)

	for _, sc := range loadtest.Scenarios() {
		for _, track := range []bool{false, true} {
			key := loadtest.FlagKey(sc, fixtures.BoolType, track)
			name := fmt.Sprintf("%s/track=%t", sc, track)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()

				for range b.N {
					require.NoError(b, evalOne(client, key, fixtures.BoolType, ctx))
				}
			})
			b.Run(name+"/parallel", func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						err := evalOne(client, key, fixtures.BoolType, ctx)
						if err != nil {
							assert.NoError(b, err)

							return
						}
					}
				})
			})
		}
	}
}
