package azblobstorageretriever

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"

	"github.com/blackbirdworks/semaphore/retriever"
)

var (
	errUnableToConnectTo = errors.New(
		"unable to connect to Azure Blob Storage, \"AccountName\" cannot be empty",
	)
	errClientIsNotInitialized            = errors.New("client is not initialized")
	errMissingMandatoryInformationObject = errors.New("missing mandatory information object")
)

// Retriever is the interface to fetch the flags from Azure Blob Storage.
type Retriever struct {
	// Container is the name of your Azure Blob Storage Container.
	Container string

	// Storage Account Name and Key
	AccountName string
	AccountKey  string

	// ServiceURL is the URL of the storage account e.g. https://<account>.blob.core.windows.net/
	// It can be overridden by the user to use a custom URL.
	// Default: https://<account>.blob.core.windows.net/
	ServiceURL string

	// Object is the name of your file in your container.
	Object string

	// client is a pointer to an Azure Blob Storage client.
	// It provides access to Azure Blob Storage services for operations like
	// creating, reading, updating, and deleting blobs.
	client *azblob.Client
	status retriever.Status
}

// Init is initializing the retriever to start fetching the flags configuration.
func (r *Retriever) Init(_ context.Context, _ *slog.Logger) error {
	if r.AccountName == "" {
		return errUnableToConnectTo
	}

	url := r.ServiceURL
	if url == "" {
		url = fmt.Sprintf("https://%s.blob.core.windows.net/", r.AccountName)
	}

	var (
		client *azblob.Client
		err    error
	)

	if r.AccountKey == "" {
		var cred *azidentity.DefaultAzureCredential

		cred, err = azidentity.NewDefaultAzureCredential(nil)
		if err != nil {
			r.status = retriever.RetrieverError

			return fmt.Errorf("create default Azure credential: %w", err)
		}

		client, err = azblob.NewClient(url, cred, nil)
	} else {
		var cred *azblob.SharedKeyCredential

		cred, err = azblob.NewSharedKeyCredential(r.AccountName, r.AccountKey)
		if err != nil {
			r.status = retriever.RetrieverError

			return fmt.Errorf("create Azure shared key credential: %w", err)
		}

		client, err = azblob.NewClientWithSharedKeyCredential(url, cred, nil)
	}

	if err != nil {
		r.status = retriever.RetrieverError

		return fmt.Errorf("create Azure blob client: %w", err)
	}

	r.client = client
	r.status = retriever.RetrieverReady

	return nil
}

// Shutdown gracefully shutdown the provider and set the status as not ready.
func (r *Retriever) Shutdown(_ context.Context) error {
	r.client = nil
	r.status = retriever.RetrieverNotReady

	return nil
}

// Status is the function returning the internal state of the retriever.
func (r *Retriever) Status() retriever.Status {
	return r.status
}

// Retrieve is the function in charge of fetching the flag configuration.
func (r *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	if r.client == nil {
		r.status = retriever.RetrieverError

		return nil, errClientIsNotInitialized
	}

	if r.Object == "" || r.Container == "" {
		return nil, fmt.Errorf("%w=%s, repositorySlug=%s", errMissingMandatoryInformationObject,
			r.Object,
			r.Container,
		)
	}

	fileStream, err := r.client.DownloadStream(ctx, r.Container, r.Object, nil)
	if err != nil {
		return nil, fmt.Errorf("download Azure blob %s: %w", r.Object, err)
	}

	retryReader := fileStream.NewRetryReader(ctx, nil)
	defer func() { _ = retryReader.Close() }()

	body, err := io.ReadAll(retryReader)
	if err != nil {
		return nil,
			fmt.Errorf(
				"unable to read from Azure Blob Storage Object %s in Container %s, error: %w",
				r.Object,
				r.Container,
				err,
			)
	}

	return body, nil
}

// compile-time check of the interface implemented by the type.
var _ retriever.InitializableRetriever = (*Retriever)(nil)
