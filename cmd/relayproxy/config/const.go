package config

import (
	"time"

	"github.com/xitongsys/parquet-go/parquet"

	ffclient "github.com/blackbirdworks/semaphore"
)

const (
	// defaultExporterFlushInterval is how often the exporters flush their events.
	defaultExporterFlushInterval = 60000 * time.Millisecond
	// defaultExporterMaxEventInMemory is the number of events kept in memory before a flush.
	defaultExporterMaxEventInMemory = 100000
)

const DefaultLogLevel = "info"

// ExporterDefaults are the values that an exporter configuration falls back to.
type ExporterDefaults struct {
	Format                  string
	LogFormat               string
	FileName                string
	CsvFormat               string
	FlushInterval           time.Duration
	MaxEventInMemory        int64
	ParquetCompressionCodec string
	LogLevel                string
	ExporterEventType       ffclient.ExporterEventType
}

// DefaultExporter returns the defaults of the exporter configuration.
func DefaultExporter() ExporterDefaults {
	return ExporterDefaults{
		Format:    "JSON",
		LogFormat: "[{{ .FormattedDate}}] user=\"{{ .UserKey}}\", flag=\"{{ .Key}}\", value=\"{{ .Value}}\"",
		FileName:  "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}",
		CsvFormat: "{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};" +
			"{{ .Value}};{{ .Default}};{{ .Source}}\n",
		FlushInterval:           defaultExporterFlushInterval,
		MaxEventInMemory:        defaultExporterMaxEventInMemory,
		ParquetCompressionCodec: parquet.CompressionCodec_SNAPPY.String(),
		LogLevel:                DefaultLogLevel,
		ExporterEventType:       ffclient.FeatureEventExporter,
	}
}
