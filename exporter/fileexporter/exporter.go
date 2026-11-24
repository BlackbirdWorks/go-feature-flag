package fileexporter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"text/template"

	"github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var _ exporter.Exporter = &Exporter{}

type Exporter struct {
	// Format is the output format you want in your exported file.
	// Available format are JSON, CSV, and Parquet.
	// Default: JSON
	Format string

	// OutputDir is the location of the directory where to store the exported files
	// Default: the current directory
	OutputDir string

	// Filename is the name of your output file
	// You can use a templated config to define the name of your export files.
	// Available replacement are {{ .Hostname}}, {{ .Timestamp}} and {{ .Format}}
	// Default: "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}"
	Filename string

	// CsvTemplate is used if your output format is CSV.
	// This field will be ignored if you are using another format than CSV.
	// You can decide which fields you want in your CSV line with a go-template syntax,
	// please check exporter/feature_event.go to see what are the fields available.
	// Default:
	// {{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};
	// {{ .Default}};{{ .Source}}\n
	CsvTemplate string

	// ParquetCompressionCodec is the parquet compression codec for better space efficiency.
	// Available options https://github.com/apache/parquet-format/blob/master/Compression.md
	// Default: SNAPPY
	ParquetCompressionCodec string

	csvTemplate      *template.Template
	filenameTemplate *template.Template
	initTemplates    sync.Once
}

// Export is saving a collection of events in a file.
func (f *Exporter) Export(
	_ context.Context,
	_ *fflog.FFLogger,
	events []exporter.ExportableEvent,
) error {
	// Parse the template only once
	f.initTemplates.Do(func() {
		f.csvTemplate = exporter.ParseTemplate(
			"csvFormat",
			f.CsvTemplate,
			exporter.DefaultCsvTemplate,
		)
		f.filenameTemplate = exporter.ParseTemplate(
			"filenameFormat",
			f.Filename,
			exporter.DefaultFilenameTemplate,
		)
	})

	// Default format for the output
	if f.Format == "" {
		f.Format = "json"
	}
	f.Format = strings.ToLower(f.Format)

	// Get the filename
	filename, err := exporter.ComputeFilename(f.filenameTemplate, f.Format)
	if err != nil {
		return err
	}

	// Handle empty OutputDir and remove trailing slash
	outputDir := strings.TrimRight(f.OutputDir, "/")
	var filePath string
	if outputDir == "" {
		filePath = filename
	} else {
		// Ensure OutputDir exists or create it
		//nolint:gosec // G302/G304: the path is chosen by the operator
		if mkdirAllErr := os.MkdirAll(outputDir, 0755); mkdirAllErr != nil {
			return fmt.Errorf("failed to create output directory: %w", mkdirAllErr)
		}
		filePath = filepath.Join(outputDir, filename)
	}

	if f.Format == "parquet" {
		return f.writeParquet(filePath, events)
	}

	return f.writeFile(filePath, events)
}

// IsBulk return false if we should directly send the data as soon as it is produce
// and true if we collect the data to send them in bulk.
func (f *Exporter) IsBulk() bool {
	return true
}

func (f *Exporter) writeFile(filePath string, events []exporter.ExportableEvent) error {
	//nolint:gosec // G302/G304: the path is chosen by the operator
	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	for _, event := range events {
		var line []byte
		var innerErr error

		// Convert the line in the right format
		switch f.Format {
		case "csv":
			line, innerErr = event.FormatInCSV(f.csvTemplate)
		case "json":
			line, innerErr = event.FormatInJSON()
		default:
			line, innerErr = event.FormatInJSON()
		}

		// Handle error and write line into the file
		if innerErr != nil {
			return fmt.Errorf("impossible to format the event in %s: %w", f.Format, innerErr)
		}
		_, errWrite := file.Write(line)
		if errWrite != nil {
			return fmt.Errorf("error while writing the export file: %w", errWrite)
		}
	}

	return nil
}

func (f *Exporter) writeParquet(filePath string, events []exporter.ExportableEvent) error {
	parquetFeatureEvents := make([]exporter.FeatureEvent, 0)
	parquetTrackingEvents := make([]exporter.TrackingEvent, 0)
	for _, event := range events {
		switch ev := any(event).(type) {
		case exporter.FeatureEvent:
			parquetFeatureEvents = append(parquetFeatureEvents, ev)
		case exporter.TrackingEvent:
			parquetTrackingEvents = append(parquetTrackingEvents, ev)
		default:
			// do nothing
		}
	}
	if len(parquetTrackingEvents) > 0 {
		return f.writeParquetTrackingEvent(filePath, parquetTrackingEvents)
	}

	return f.writeParquetFeatureEvent(filePath, parquetFeatureEvents)
}

// writeParquetFeatureEvent writes the feature events in a parquet file.
func (f *Exporter) writeParquetFeatureEvent(filePath string, events []exporter.FeatureEvent) error {
	fw, err := local.NewLocalFileWriter(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = fw.Close() }()

	pw, err := writer.NewParquetWriter(fw, new(exporter.FeatureEvent), int64(runtime.NumCPU()))
	if err != nil {
		return err
	}

	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	if ct, compressionCodecFromStringErr := parquet.CompressionCodecFromString(
		f.ParquetCompressionCodec,
	); compressionCodecFromStringErr == nil {
		pw.CompressionType = ct
	}

	for _, event := range events {
		eventValue, convertValueForParquetErr := event.ConvertValueForParquet()
		if convertValueForParquetErr != nil {
			return convertValueForParquetErr
		}
		event.Value = eventValue
		if convertValueForParquetErr = pw.Write(event); convertValueForParquetErr != nil {
			return fmt.Errorf("error while writing the parquet export file: %w", convertValueForParquetErr)
		}
	}

	return pw.WriteStop()
}

// writeParquetTrackingEvent writes the tracking events in a parquet file.
func (f *Exporter) writeParquetTrackingEvent(
	filePath string,
	events []exporter.TrackingEvent,
) error {
	fw, err := local.NewLocalFileWriter(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = fw.Close() }()

	pw, err := writer.NewParquetWriter(fw, new(exporter.TrackingEvent), int64(runtime.NumCPU()))
	if err != nil {
		return err
	}

	pw.CompressionType = parquet.CompressionCodec_SNAPPY
	if ct, compressionCodecFromStringErr := parquet.CompressionCodecFromString(
		f.ParquetCompressionCodec,
	); compressionCodecFromStringErr == nil {
		pw.CompressionType = ct
	}

	for _, event := range events {
		if err = pw.Write(event); err != nil {
			return fmt.Errorf("error while writing the parquet export file: %w", err)
		}
	}

	return pw.WriteStop()
}
