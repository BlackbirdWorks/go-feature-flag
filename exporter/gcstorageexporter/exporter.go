package gcstorageexporter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	//nolint:staticcheck,revive // ST1005: the text is observable
	errYouShouldSpecifyA = errors.New(
		"you should specify a bucket.",
	)
)

var _ exporter.Exporter = &Exporter{}

type Exporter struct {
	// Bucket is the name of your S3 Bucket.
	Bucket string
	// Format is the output format you want in your exported file.
	// Available format are JSON, CSV, and Parquet.
	// Default: JSON
	Format string
	// Path allows you to specify in which directory you want to export your data.
	Path string
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
	// {{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}}\n
	CsvTemplate string
	// ParquetCompressionCodec is the parquet compression codec for better space efficiency.
	// Available options https://github.com/apache/parquet-format/blob/master/Compression.md
	// Default: SNAPPY
	ParquetCompressionCodec string
	// Options are Google Cloud Api options to connect to Google Storage SDK
	Options []option.ClientOption
}

func (f *Exporter) IsBulk() bool {
	return true
}

// Export is saving a collection of events in a file.
func (f *Exporter) Export(
	ctx context.Context,
	logger *fflog.FFLogger,
	events []exporter.ExportableEvent,
) error {
	// Init google storage client
	client, err := storage.NewClient(ctx, f.Options...)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if f.Bucket == "" {
		return fmt.Errorf("%w %v is invalid", errYouShouldSpecifyA, f.Bucket)
	}

	// Create a temp directory to store the file we will produce
	outputDir, err := os.MkdirTemp("", "semaphore_GoogleCloudStorage_export")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(outputDir) }()

	// We call the File data exporter to get the file in the right format.
	// Files will be put in the temp directory, so we will be able to upload them to S3 from there.
	fileExporter := fileexporter.Exporter{
		Format:                  f.Format,
		OutputDir:               outputDir,
		Filename:                f.Filename,
		CsvTemplate:             f.CsvTemplate,
		ParquetCompressionCodec: f.ParquetCompressionCodec,
	}
	err = fileExporter.Export(ctx, logger, events)
	if err != nil {
		return err
	}

	// Upload all the files in the folder to google storage
	files, err := os.ReadDir(outputDir)
	if err != nil {
		return err
	}

	for _, file := range files {
		fileHandle, openErr := os.Open(path.Clean(outputDir + "/" + file.Name()))
		if openErr != nil {
			logger.ErrorContext(ctx, "[GCP Exporter] impossible to open the file",
				slog.String("path", outputDir+"/"+file.Name()))

			continue
		}
		uploadFileErr := f.uploadFile(ctx, client, fileHandle, file.Name())
		if uploadFileErr != nil {
			return uploadFileErr
		}
	}

	return nil
}

// uploadFile uploads a single file to Google Cloud Storage and closes the file
// handle before returning. Keeping the defer here (instead of in the calling loop)
// ensures at most one file descriptor is open at a time, regardless of batch size.
func (f *Exporter) uploadFile(
	ctx context.Context,
	client *storage.Client,
	fileHandle *os.File,
	fileName string,
) error {
	defer func() { _ = fileHandle.Close() }()

	// prepend the path
	source := fileName
	if f.Path != "" {
		source = path.Clean(f.Path + "/" + fileName)
	}

	wc := client.Bucket(f.Bucket).Object(source).NewWriter(ctx)
	_, err := io.Copy(wc, fileHandle)
	// The GCS writer buffers the payload and flushes it on Close, so upload
	// failures surface from Close rather than from io.Copy. Capture both so a
	// failed upload is reported instead of silently swallowed.
	closeErr := wc.Close()
	if closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf(
			"error: [GCP Exporter] impossible to copy the file from %s to bucket %s: %w",
			source,
			f.Bucket,
			err,
		)
	}

	return nil
}
