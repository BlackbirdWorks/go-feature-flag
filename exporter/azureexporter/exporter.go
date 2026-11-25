package azureexporter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/exporter/fileexporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	//nolint:staticcheck,revive // ST1005: the text is observable
	errYouShouldSpecifyAn = errors.New(
		"you should specify an AccountName.",
	)
	//nolint:staticcheck,revive // ST1005: the text is observable
	errYouShouldSpecifyA = errors.New(
		"you should specify a container.",
	)
)

var _ exporter.Exporter = &Exporter{}

// Exporter sends evaluation events to Azure Blob Storage.
type Exporter struct {
	// Container is the name of your Azure Blob Storage Container similar to Buckets in S3.
	Container string

	// Storage Account Name and Key
	AccountName string
	AccountKey  string

	Format                  string
	Path                    string
	Filename                string
	CsvTemplate             string
	ParquetCompressionCodec string

	// ServiceURL is the URL of the storage account e.g. https://<account>.blob.core.windows.net/
	// It can be overridden by the user to use a custom URL.
	// Default: https://<account>.blob.core.windows.net/
	ServiceURL string
}

// Export writes the events to Azure Blob Storage.
func (f *Exporter) Export(
	ctx context.Context,
	logger *fflog.FFLogger,
	featureEvents []exporter.ExportableEvent,
) error {
	if f.AccountName == "" {
		return fmt.Errorf("%w %v is invalid", errYouShouldSpecifyAn, f.AccountName)
	}

	client, err := f.initializeAzureClient()
	if err != nil {
		return err
	}

	if f.Container == "" {
		return fmt.Errorf("%w %v is invalid", errYouShouldSpecifyA, f.Container)
	}

	outputDir, err := os.MkdirTemp("", "semaphore_AzureBlobStorage_export")
	if err != nil {
		return fmt.Errorf("create temp dir for Azure export: %w", err)
	}
	defer func() { _ = os.RemoveAll(outputDir) }()

	fileExporter := fileexporter.Exporter{
		Format:                  f.Format,
		OutputDir:               outputDir,
		Filename:                f.Filename,
		CsvTemplate:             f.CsvTemplate,
		ParquetCompressionCodec: f.ParquetCompressionCodec,
	}

	err = fileExporter.Export(ctx, logger, featureEvents)
	if err != nil {
		return fmt.Errorf("export events to temp file for Azure: %w", err)
	}

	files, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("read temp dir for Azure export: %w", err)
	}

	for _, file := range files {
		fileName := file.Name()

		fileHandle, openErr := os.Open(path.Clean(outputDir + "/" + fileName))
		if openErr != nil {
			logger.ErrorContext(
				ctx,
				"[Azure Exporter] impossible to open file",
				slog.String("path", outputDir+"/"+fileName),
			)

			continue
		}

		uploadFileErr := f.uploadFile(ctx, client, logger, fileHandle, outputDir, fileName)
		if uploadFileErr != nil {
			return uploadFileErr
		}
	}

	return nil
}

// IsBulk reports whether the exporter sends events in bulk.
func (*Exporter) IsBulk() bool {
	return true
}

func (f *Exporter) initializeAzureClient() (*azblob.Client, error) {
	url := fmt.Sprintf("https://%s.blob.core.windows.net/", f.AccountName)
	if f.ServiceURL != "" {
		url = f.ServiceURL
	}

	if f.AccountKey == "" {
		cred, err := azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			return nil, fmt.Errorf("create default Azure credential: %w", err)
		}

		client, err := azblob.NewClient(url, cred, nil)
		if err != nil {
			return nil, fmt.Errorf("create Azure blob client: %w", err)
		}

		return client, nil
	}

	cred, err := azblob.NewSharedKeyCredential(f.AccountName, f.AccountKey)
	if err != nil {
		return nil, fmt.Errorf("create Azure shared key credential: %w", err)
	}

	client, err := azblob.NewClientWithSharedKeyCredential(url, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("create Azure blob client with shared key: %w", err)
	}

	return client, nil
}

// uploadFile uploads a single file to Azure Blob Storage and closes the file
// handle before returning. Keeping the defer here (instead of in the calling
// loop) ensures at most one file descriptor is open at a time, regardless of
// batch size.
func (f *Exporter) uploadFile(
	ctx context.Context,
	client *azblob.Client,
	logger *fflog.FFLogger,
	fileHandle *os.File,
	outputDir, fileName string,
) error {
	defer func() { _ = fileHandle.Close() }()

	// prepend the path
	source := fileName
	if f.Path != "" {
		source = f.Path + "/" + fileName
	}

	_, err := client.UploadFile(ctx, f.Container, source, fileHandle, nil)
	if err != nil {
		logger.ErrorContext(
			ctx,
			"[Azure Exporter] failed to upload file",
			slog.String("path", outputDir+"/"+fileName),
		)

		return fmt.Errorf("upload file to Azure blob: %w", err)
	}

	logger.InfoContext(
		ctx,
		"[Azure Exporter] file uploaded.",
		slog.String("location", f.Container+"/"+fileName),
	)

	return nil
}
