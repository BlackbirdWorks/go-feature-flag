package loadtest

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
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
func NewLibraryTarget(o LibraryOptions) (*LibraryTarget, error) {
	dir, err := os.MkdirTemp("", "ffsoak")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "flags.yaml")
	if writeFileErr := os.WriteFile(path, []byte(FlagsYAML()), 0o600); writeFileErr != nil {
		return nil, writeFileErr
	}
	if o.FlushInterval == 0 {
		o.FlushInterval = time.Second
	}
	if o.MaxEventInMemory == 0 {
		o.MaxEventInMemory = 100000
	}
	exp := NewExporter(o.ExporterMode)
	c, err := ffclient.New(ffclient.Config{
		PollingInterval: time.Hour,
		Retriever:       &fileretriever.Retriever{Path: path},
		DataExporter: ffclient.DataExporter{
			FlushInterval:    o.FlushInterval,
			MaxEventInMemory: o.MaxEventInMemory,
			Exporter:         exp,
		},
	})
	if err != nil {
		return nil, err
	}

	return &LibraryTarget{client: c, exporter: exp, flagKey: o.FlagKey, dir: dir}, nil
}

// Eval implements Target.
func (l *LibraryTarget) Eval(worker, i int) error {
	u := (worker*7919 + i) % 1000
	ctx := ffcontext.NewEvaluationContextBuilder(fmt.Sprintf("user-%d", u)).
		AddCustom("email", "user@example.com").
		AddCustom("country", "FR").
		AddCustom("age", 34).
		AddCustom("plan", "pro").
		AddCustom("region", "eu-west-1").
		AddCustom("beta", true).
		AddCustom("version", "1.42.0").
		AddCustom("device", "ios").
		AddCustom("company", "acme").
		Build()
	_, err := l.client.BoolVariation(l.flagKey, ctx, false)

	return err
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
