package mockretriever

import (
	"context"
	"errors"
	"log/slog"

	"github.com/blackbirdworks/semaphore/retriever"
)

var (
	errInitializationFailed = errors.New("initialization failed")
)

// InitializableRetriever implements the standard InitializableRetriever interface.
type InitializableRetriever struct {
	ReceivedLogger *slog.Logger
	Name           string
	InitShouldFail bool
	RetrieveCalled bool
	InitCalled     bool
	ShutdownCalled bool
	StatusCalled   bool
}

// NewInitializableRetriever returns a mock retriever with the given name.
func NewInitializableRetriever(name string) *InitializableRetriever {
	return &InitializableRetriever{Name: name}
}

// Retrieve returns the mock flag configuration.
func (m *InitializableRetriever) Retrieve(_ context.Context) ([]byte, error) {
	m.RetrieveCalled = true

	return []byte(defaultFlagConfig), nil
}

// Init initializes the mock retriever.
func (m *InitializableRetriever) Init(_ context.Context, logger *slog.Logger) error {
	m.InitCalled = true

	m.ReceivedLogger = logger
	if m.InitShouldFail {
		return errInitializationFailed
	}

	return nil
}

// Shutdown stops the mock retriever.
func (m *InitializableRetriever) Shutdown(_ context.Context) error {
	m.ShutdownCalled = true

	return nil
}

// Status returns the current status of the mock retriever.
func (m *InitializableRetriever) Status() retriever.Status {
	m.StatusCalled = true
	if m.InitShouldFail {
		return retriever.RetrieverError
	}

	return retriever.RetrieverReady
}

// GetName returns the name of the mock retriever.
func (m *InitializableRetriever) GetName() string {
	return m.Name
}

// compile-time check of the interface implemented by the type.
var _ retriever.InitializableRetriever = (*InitializableRetriever)(nil)
