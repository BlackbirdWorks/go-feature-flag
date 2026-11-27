package flagruntime_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

const (
	repoRoot    = "../../../"
	shapesFile  = "testdata/shapes.yaml"
	setKey      = "key-equiv"
	setName     = "equiv"
	missingKey  = "flag-that-does-not-exist"
	flagsName   = "flags"
	planAttr    = "plan"
	proPlan     = "pro"
	boolStatic  = "bool-static"
	kindObject  = "object"
	kindArray   = "array"
	nameKey     = "name"
	orgKey      = "org"
	prodEnv     = "prod"
	boolRule    = "bool-rule"
	objectFlag  = "object-flag"
	acmeOrg     = "acme"
	pendingSet  = "pending"
	freePlan    = "free"
	mutated     = "mutated"
	trackedFlag = "bool-tracked"
	unknownStr  = "unknown"
)

// formatOf is the format of a flag file, by its extension.
func formatOf(path string) string {
	switch filepath.Ext(path) {
	case ".json":
		return controlplane.FormatJSON
	case ".toml":
		return controlplane.FormatTOML
	default:
		return controlplane.FormatYAML
	}
}

// collector keeps the evaluation events that the version 1 client exports.
type collector struct {
	events []*exporter.FeatureEvent
	mu     sync.Mutex
}

func (*collector) IsBulk() bool { return true }

func (c *collector) Export(_ context.Context, _ *slog.Logger, events []exporter.ExportableEvent) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, event := range events {
		if fe, ok := event.(*exporter.FeatureEvent); ok {
			c.events = append(c.events, fe)
		}
	}

	return nil
}

// versionOne starts a version 1 client on a flag file. It exports to sink when sink is not nil.
func versionOne(
	tb testing.TB, path string, enrichment map[string]any, env string, sink *collector,
) *ffclient.GoFeatureFlag {
	tb.Helper()

	cfg := ffclient.Config{
		PollingInterval:             time.Hour,
		Retriever:                   &fileretriever.Retriever{Path: path},
		FileFormat:                  formatOf(path),
		EvaluationContextEnrichment: enrichment,
		Environment:                 env,
	}

	if sink != nil {
		cfg.DataExporter = ffclient.DataExporter{
			FlushInterval: time.Hour, MaxEventInMemory: 100000, Exporter: sink,
		}
	}

	client, err := ffclient.New(&cfg)
	require.NoError(tb, err)

	tb.Cleanup(client.Close)

	return client
}

// runtimeOf builds the runtime of one flags object (one flag set, one key) and returns the flag set.
func runtimeOf(tb testing.TB, data []byte, format string) *flagruntime.FlagSet {
	tb.Helper()

	return runtimeWith(tb, objs{
		flagsName: obj(flagruntime.KindFlags, data),
		setsName:  setsObj(tb, fset(setName, src(flagsName), setKey)),
	}.formatted(flagsName, format)).FlagSets[setName]
}

func (in objs) formatted(name, format string) objs {
	out := in.with(nil)
	held := out[name]
	held.Format = format
	out[name] = held

	return out
}

// runtimeWith builds the runtime of a content at generation 1.
func runtimeWith(tb testing.TB, in objs) *flagruntime.Runtime {
	tb.Helper()

	builder := flagruntime.NewBuilder(newService(tb), flagruntime.BuilderOptions{})

	built, err := builder.Build(context.Background(), content(1, in))
	require.NoError(tb, err)

	return built.Runtime
}

// flagNamesOf lists the flags of a flag file, as the engine parser reads them.
func flagNamesOf(tb testing.TB, data []byte, format string) []string {
	tb.Helper()

	raw, err := cache.ConvertToFlagStruct(data, format)
	require.NoError(tb, err)

	names := make([]string, 0, len(raw)+1)
	for name := range raw {
		names = append(names, name)
	}

	return append(sortedStrings(names), missingKey)
}

func readFile(tb testing.TB, path string) []byte {
	tb.Helper()

	data, err := os.ReadFile(path)
	require.NoError(tb, err)

	return data
}

// atInstant is a context that evaluates at the instant of the frozen clock plus a delta.
func atInstant(key string, delta time.Duration) ffcontext.EvaluationContext {
	return ffcontext.NewEvaluationContextBuilder(key).
		AddCustom("country", "FR").
		AddCustom(ffcontext.ReservedKey, map[string]any{
			"currentDateTime": clocktest.Instant().Add(delta).Format(time.RFC3339),
		}).
		Build()
}

// equivalenceContexts are the evaluation contexts of the equivalence tables.
func equivalenceContexts() []ffcontext.Context {
	ctxs := make([]ffcontext.Context, 0, 24)
	ctxs = append(ctxs,
		nil,
		ffcontext.NewEvaluationContextBuilder("env-user").AddCustom("env", "staging").AddCustom("beta", true).Build(),
		ffcontext.NewEvaluationContext(""),
		ffcontext.NewEvaluationContext("user-1"),
		ffcontext.NewEvaluationContextBuilder("user-2").
			AddCustom("beta", true).
			AddCustom("country", "FR").AddCustom(planAttr, proPlan).AddCustom("teamId", "t1").Build(),
		ffcontext.NewEvaluationContextBuilder("user-3").
			AddCustom("beta", false).
			AddCustom("country", "US").AddCustom(planAttr, freePlan).AddCustom("teamId", "t2").Build(),
		ffcontext.NewEvaluationContextBuilder("random-key").AddCustom("beta", "true").Build(),
		ffcontext.NewEvaluationContextBuilder("anon").AddCustom("anonymous", true).Build(),
		ffcontext.NewEvaluationContextBuilder("no-key-with-team").AddCustom("teamId", "t3").Build(),
		atInstant("dated-1", -30*24*time.Hour),
		atInstant("dated-2", -7*24*time.Hour),
		atInstant("dated-3", 0),
		atInstant("dated-4", 7*24*time.Hour),
		atInstant("dated-5", 40*24*time.Hour),
	)

	for idx := range 8 {
		ctxs = append(ctxs, ffcontext.NewEvaluationContextBuilder("pct-"+string(rune('a'+idx))).
			AddCustom("country", "FR").AddCustom("teamId", "team-"+string(rune('a'+idx))).Build())
	}

	return ctxs
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o600)
}

// keyOf is the targeting key of a context, for the messages ("nil" for the nil context).
func keyOf(ctx ffcontext.Context) string {
	if ctx == nil {
		return "nil"
	}

	return ctx.GetKey()
}

// minPairs is the least number of flag and context pairs the single evaluation table checks.
const minPairs = 3800
