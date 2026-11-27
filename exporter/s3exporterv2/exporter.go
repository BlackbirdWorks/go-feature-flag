package s3exporterv2

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/internal"
)

var _ exporter.Exporter = &Exporter{}

// Exporter uploads evaluation events to Amazon S3.
type Exporter struct {
	s3Uploader UploaderAPI
	// loadConfig loads the configuration of the default AWS chain when AwsConfig is nil. It is nil in production (then
	// config.LoadDefaultConfig) and is replaced only by tests: an exporter that is given its configuration, as the
	// relay proxy does from the connection factory, never calls it.
	loadConfig func(context.Context) (aws.Config, error)
	// AwsConfig is the AWS SDK configuration object we will use to
	// upload your exported data files.
	AwsConfig *aws.Config
	// Bucket is the name of your Exporter Bucket.
	Bucket string
	// Format is the output format you want in your exported file.
	// Available format are JSON, CSV and Parquet.
	// Default: JSON
	Format string
	// S3Path allows you to specify in which directory you want to export your data.
	S3Path string
	// Filename is the name of your output file
	// You can use a templated config to define the name of your export files.
	// Available replacement are {{ .Hostname}}, {{ .Timestamp}} and {{ .Format}}
	// Default: "flag-variation-{{ .Hostname}}-{{ .Timestamp}}.{{ .Format}}"
	Filename string
	// CsvTemplate is used if your output format is CSV.
	// This field will be ignored if you are using another format than CSV.
	// You can decide which fields you want in your CSV line with a go-template syntax,
	// please check internal/exporter/feature_event.go to see what are the fields available.
	// Default:
	// {{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .Key}};{{ .Variation}};{{ .Value}};{{ .Default}}\n
	CsvTemplate string
	// ParquetCompressionCodec is the parquet compression codec for better space efficiency.
	// Available options https://github.com/apache/parquet-format/blob/master/Compression.md
	// Default: SNAPPY
	ParquetCompressionCodec string
	// S3Client is an optional ready-made S3 client (for example the one of a connection factory). When it is set
	// the exporter uses it as it is: AwsConfig and S3ClientOptions are not used to build another client.
	S3Client *s3.Client
	// S3ClientOptions is a list of functional options to configure the S3 client.
	// Provide additional functional options to further configure the behavior of the client,
	// such as changing the client's endpoint or adding custom middleware behavior.
	// For more information about the options, please check:
	// https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3#Options
	S3ClientOptions []func(*s3.Options)
	// initMu guards the initialisation, which is retried by the next call until it has succeeded.
	initMu   sync.Mutex
	initDone bool
}

// Export is saving a collection of events in a file.
func (f *Exporter) Export(
	ctx context.Context,
	logger *slog.Logger,
	events []exporter.ExportableEvent,
) error {
	ctx = internal.ContextOrBackground(ctx)

	// always through the initialiser: it takes the lock, so s3Uploader is never read while another call writes it
	initErr := f.initializeUploader(ctx)
	if initErr != nil {
		return initErr
	}

	// Create a temp directory to store the file we will produce
	outputDir, err := os.MkdirTemp("", "semaphore_s3_export")
	if err != nil {
		return fmt.Errorf("create temp dir for S3 export: %w", err)
	}
	defer func() { _ = os.RemoveAll(outputDir) }()

	// We call the File data exporter to get the file in the right format.
	// Files will be put in the temp directory, so we will be able to upload them to export from there.
	fileExporter := fileexporter.Exporter{
		Format:                  f.Format,
		OutputDir:               outputDir,
		Filename:                f.Filename,
		CsvTemplate:             f.CsvTemplate,
		ParquetCompressionCodec: f.ParquetCompressionCodec,
	}

	err = fileExporter.Export(ctx, logger, events)
	if err != nil {
		return fmt.Errorf("export events to temp file for S3: %w", err)
	}

	// Upload all the files in the folder to export
	files, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("read temp dir for S3 export: %w", err)
	}

	for _, file := range files {
		// read file
		fileHandle, openErr := os.Open(outputDir + "/" + file.Name())
		if openErr != nil {
			logger.ErrorContext(
				ctx,
				"[S3Exporter] impossible to open the file",
				slog.String("path", outputDir+"/"+file.Name()),
			)

			continue
		}

		uploadFileErr := f.uploadFile(ctx, logger, fileHandle, file.Name())
		if uploadFileErr != nil {
			return uploadFileErr
		}
	}

	return nil
}

// IsBulk returns true: events are buffered and written to S3 in batches, one file per flush.
func (*Exporter) IsBulk() bool {
	return true
}

func (f *Exporter) initializeUploader(ctx context.Context) error {
	f.initMu.Lock()
	defer f.initMu.Unlock()

	if f.initDone {
		return nil
	}

	if f.s3Uploader != nil { // an uploader that was set up by hand (a test double) is kept
		f.initDone = true

		return nil
	}

	if f.S3Client != nil {
		f.s3Uploader = transfermanager.New(f.S3Client)
		f.initDone = true

		return nil
	}

	if f.AwsConfig == nil {
		cfg, err := f.defaultConfig(ctx)
		if err != nil {
			return fmt.Errorf("impossible to init S3 exporter: %w", err)
		}

		f.AwsConfig = &cfg
	}

	client := s3.NewFromConfig(*f.AwsConfig, f.S3ClientOptions...)
	f.s3Uploader = transfermanager.New(client)
	f.initDone = true

	return nil
}

// uploadFile uploads a single file to S3 and closes the file handle before
// returning. Keeping the defer here (instead of in the calling loop) ensures at
// most one file descriptor is open at a time, regardless of batch size.
func (f *Exporter) uploadFile(
	ctx context.Context,
	logger *slog.Logger,
	fileHandle *os.File,
	fileName string,
) error {
	defer func() { _ = fileHandle.Close() }()

	result, err := f.s3Uploader.UploadObject(ctx, &transfermanager.UploadObjectInput{
		Bucket: aws.String(f.Bucket),
		Key:    aws.String(f.S3Path + "/" + fileName),
		Body:   fileHandle,
	})
	if err != nil {
		return fmt.Errorf("upload export file to S3: %w", err)
	}

	location := ""
	if result.Location != nil {
		location = *result.Location
	}

	logger.InfoContext(ctx, "[S3Exporter] file uploaded.", slog.String("location", location))

	return nil
}

// defaultConfig loads the configuration of the default AWS chain; only an exporter used as a library, without an
// AwsConfig, reaches it.
func (f *Exporter) defaultConfig(ctx context.Context) (aws.Config, error) {
	if f.loadConfig != nil {
		return f.loadConfig(ctx)
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load the default AWS configuration: %w", err)
	}

	return cfg, nil
}
