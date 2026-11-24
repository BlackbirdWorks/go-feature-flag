//go:build bench

package ffclient_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

var (
	scenarioOnce   sync.Once
	scenarioClient *ffclient.GoFeatureFlag
)

func newBenchClient(b *testing.B, yaml string, withExporter bool) *ffclient.GoFeatureFlag {
	b.Helper()
	dir, err := os.MkdirTemp("", "ffbench")
	if err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(dir, "flags.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		b.Fatal(err)
	}
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
	c, err := ffclient.New(cfg)
	if err != nil {
		b.Fatal(err)
	}
	return c
}

func getScenarioClient(b *testing.B) *ffclient.GoFeatureFlag {
	b.Helper()
	scenarioOnce.Do(func() { scenarioClient = newBenchClient(b, loadtest.FlagsYAML(), true) })
	return scenarioClient
}

// benchContext builds a realistic evaluation context (about 10 attributes).
func benchContext(i int) ffcontext.Context {
	return ffcontext.NewEvaluationContextBuilder(fmt.Sprintf("user-%d", i%1000)).
		AddCustom("email", fmt.Sprintf("user-%d@example.com", i%1000)).
		AddCustom("country", "FR").
		AddCustom("age", 34).
		AddCustom("plan", "pro").
		AddCustom("region", "eu-west-1").
		AddCustom("beta", true).
		AddCustom("version", "1.42.0").
		AddCustom("device", "ios").
		AddCustom("company", "acme").
		Build()
}

func evalOne(c *ffclient.GoFeatureFlag, key, typ string, ctx ffcontext.Context) error {
	var err error
	switch typ {
	case "string":
		_, err = c.StringVariation(key, ctx, "x")
	case "json":
		_, err = c.JSONVariation(key, ctx, map[string]any{})
	default:
		_, err = c.BoolVariation(key, ctx, false)
	}
	return err
}

// BenchmarkScenarios evaluates every scenario/type with event tracking OFF and ON, sequentially.
func BenchmarkScenarios(b *testing.B) {
	c := getScenarioClient(b)
	for _, sc := range loadtest.Scenarios {
		for _, typ := range loadtest.Types {
			for _, track := range []bool{false, true} {
				// to keep the matrix readable, only bool is run for every scenario;
				// string and json are run for the no-rule and the targeting scenarios.
				if typ != "bool" && sc != "norule" && sc != "rule" {
					continue
				}
				key := loadtest.FlagKey(sc, typ, track)
				name := fmt.Sprintf("%s/%s/track=%t", sc, typ, track)
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					for i := range b.N {
						if err := evalOne(c, key, typ, benchContext(i)); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

// BenchmarkScenariosParallel is the same matrix as BenchmarkScenarios using all cores.
func BenchmarkScenariosParallel(b *testing.B) {
	c := getScenarioClient(b)
	for _, sc := range loadtest.Scenarios {
		for _, track := range []bool{false, true} {
			key := loadtest.FlagKey(sc, "bool", track)
			b.Run(fmt.Sprintf("%s/bool/track=%t", sc, track), func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					i := 0
					for pb.Next() {
						i++
						if err := evalOne(c, key, "bool", benchContext(i)); err != nil {
							b.Error(err)
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
	for _, n := range []int{10, 100, 1000} {
		c := newBenchClient(b, loadtest.ManyFlagsYAML(n), false)
		b.Run(fmt.Sprintf("flags=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := range b.N {
				s := c.AllFlagsState(benchContext(i))
				if len(s.GetFlags()) != n {
					b.Fatalf("expected %d flags got %d", n, len(s.GetFlags()))
				}
			}
		})
		b.Run(fmt.Sprintf("flags=%d/parallel", n), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					i++
					_ = c.AllFlagsState(benchContext(i))
				}
			})
		})
		c.Close()
	}
}

// BenchmarkScenariosPrebuiltContext measures the engine alone: the evaluation context (about 10
// attributes) is built once and shared, as a service that reuses its contexts would do.
func BenchmarkScenariosPrebuiltContext(b *testing.B) {
	c := getScenarioClient(b)
	ctx := benchContext(5)
	for _, sc := range loadtest.Scenarios {
		for _, track := range []bool{false, true} {
			key := loadtest.FlagKey(sc, "bool", track)
			name := fmt.Sprintf("%s/track=%t", sc, track)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if err := evalOne(c, key, "bool", ctx); err != nil {
						b.Fatal(err)
					}
				}
			})
			b.Run(name+"/parallel", func(b *testing.B) {
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if err := evalOne(c, key, "bool", ctx); err != nil {
							b.Error(err)
							return
						}
					}
				})
			})
		}
	}
}
