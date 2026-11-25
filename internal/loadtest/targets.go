package loadtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

const (
	// distinctUsers is the number of distinct users of the evaluation contexts, userStride spreads the workers over them.
	distinctUsers = 1000
	userStride    = 7919
	// sampleAge is the value of the custom "age" attribute of the evaluation contexts.
	sampleAge = 34
)

// LibraryTarget evaluates a bool flag through the embedded library with event tracking.
type LibraryTarget struct {
	client   *ffclient.GoFeatureFlag
	exporter *Exporter
	flagKey  string
	dir      string
}

// LibraryOptions configure NewLibraryTarget.
type LibraryOptions struct {
	FlagKey          string
	ExporterMode     ExporterMode
	FlushInterval    time.Duration
	MaxEventInMemory int64
}

// NewLibraryTarget starts a library client on the generated flags.
func NewLibraryTarget(ctx context.Context, opts LibraryOptions) (*LibraryTarget, error) {
	dir, err := os.MkdirTemp("", "ffsoak")
	if err != nil {
		return nil, fmt.Errorf("create soak temp dir: %w", err)
	}

	path := filepath.Join(dir, "flags.yaml")

	writeFileErr := os.WriteFile(path, []byte(FlagsYAML()), 0o600)
	if writeFileErr != nil {
		return nil, fmt.Errorf("write soak flag file: %w", writeFileErr)
	}

	if opts.FlushInterval == 0 {
		opts.FlushInterval = time.Second
	}

	if opts.MaxEventInMemory == 0 {
		opts.MaxEventInMemory = 100000
	}

	exp := NewExporter(opts.ExporterMode)

	client, err := ffclient.NewWithContext(ctx, &ffclient.Config{
		PollingInterval: time.Hour,
		Retriever:       &fileretriever.Retriever{Path: path},
		DataExporter: ffclient.DataExporter{
			FlushInterval:    opts.FlushInterval,
			MaxEventInMemory: opts.MaxEventInMemory,
			Exporter:         exp,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("init soak ffclient: %w", err)
	}

	return &LibraryTarget{client: client, exporter: exp, flagKey: opts.FlagKey, dir: dir}, nil
}

// Eval implements Target.
func (l *LibraryTarget) Eval(worker, i int) error {
	u := (worker*userStride + i) % distinctUsers
	ctx := ffcontext.NewEvaluationContextBuilder(fmt.Sprintf("user-%d", u)).
		AddCustom("email", "user@example.com").
		AddCustom("country", "FR").
		AddCustom("age", sampleAge).
		AddCustom("plan", "pro").
		AddCustom("region", "eu-west-1").
		AddCustom("beta", true).
		AddCustom("version", "1.42.0").
		AddCustom("device", "ios").
		AddCustom("company", "acme").
		Build()

	_, err := l.client.BoolVariation(l.flagKey, ctx, false)
	if err != nil {
		return fmt.Errorf("evaluate bool flag %s: %w", l.flagKey, err)
	}

	return nil
}

// Extra implements Target.
func (l *LibraryTarget) Extra() map[string]int64 {
	return map[string]int64{
		"exported_events":  l.exporter.Events.Load(),
		"exported_batches": l.exporter.Batches.Load(),
		"failed_batches":   l.exporter.Failures.Load(),
		"dropped_events":   l.client.DroppedEvents(),
	}
}

// Close implements Target.
func (l *LibraryTarget) Close() {
	l.client.Close()
	_ = os.RemoveAll(l.dir)
}
