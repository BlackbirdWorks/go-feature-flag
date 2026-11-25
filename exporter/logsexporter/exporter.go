package logsexporter

import (
	"context"
	"fmt"
	"sync"
	"text/template"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var _ exporter.Exporter = &Exporter{}

const defaultLoggerFormat = "[{{ .FormattedDate}}] user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", value=\"{{ .Value}}\""

// Exporter writes evaluation events to the logs.
type Exporter struct {
	logTemplate *template.Template
	// Format is the template configuration of the output format of your log.
	// You can use all the key from the exporter.FeatureEvent + a key called FormattedDate that represent the date with
	// the RFC 3339 Format
	// Default: [{{ .FormattedDate}}] user="{{ .UserKey}}", flag="{{ .Key}}", value="{{ .Value}}"
	Format string // Deprecated: use LogFormat instead.
	// Format is the template configuration of the output format of your log.
	// You can use all the key from the exporter.FeatureEvent + a key called FormattedDate that represent the date with
	// the RFC 3339 Format
	// Default: [{{ .FormattedDate}}] user="{{ .UserKey}}", flag="{{ .Key}}", value="{{ .Value}}"
	LogFormat     string
	initTemplates sync.Once
}

// Export is saving a collection of events in a file.
func (f *Exporter) Export(
	ctx context.Context,
	logger *fflog.FFLogger,
	events []exporter.ExportableEvent,
) error {
	f.initTemplates.Do(func() {
		// Remove below after deprecation of Format
		if f.LogFormat == "" && f.Format != "" {
			f.LogFormat = f.Format
		}

		f.logTemplate = exporter.ParseTemplate("logFormat", f.LogFormat, defaultLoggerFormat)
	})

	for _, event := range events {
		log, err := event.FormatInCSV(f.logTemplate)
		if err != nil {
			return fmt.Errorf("format log event: %w", err)
		}

		logger.InfoContext(ctx, string(log))
	}

	return nil
}

// IsBulk reports whether the exporter sends events in bulk.
func (_ *Exporter) IsBulk() bool {
	return false
}
