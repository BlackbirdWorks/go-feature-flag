package mockretriever

import (
	"context"
	"log"

	"github.com/blackbirdworks/semaphore/retriever"
)

// InitializableRetrieverLegacy implements the standard InitializableRetriever interface.
type InitializableRetrieverLegacy struct {
	ReceivedLogger *log.Logger
	Name           string
	InitShouldFail bool
	RetrieveCalled bool
	InitCalled     bool
	ShutdownCalled bool
	StatusCalled   bool
}

// NewInitializableRetrieverLegacy returns a mock retriever with the given name that uses the standard library logger.
func NewInitializableRetrieverLegacy(name string) *InitializableRetrieverLegacy {
	return &InitializableRetrieverLegacy{Name: name}
}

// Retrieve returns the mock flag configuration.
func (m *InitializableRetrieverLegacy) Retrieve(_ context.Context) ([]byte, error) {
	m.RetrieveCalled = true

	return []byte(defaultFlagConfig), nil
}

// Init initializes the mock retriever with the standard library logger.
func (m *InitializableRetrieverLegacy) Init(_ context.Context, logger *log.Logger) error {
	m.InitCalled = true

	m.ReceivedLogger = logger
	if m.InitShouldFail {
		return errInitializationFailed
	}

	return nil
}

// Shutdown stops the mock retriever.
func (m *InitializableRetrieverLegacy) Shutdown(_ context.Context) error {
	m.ShutdownCalled = true

	return nil
}

// Status returns the current status of the mock retriever.
func (m *InitializableRetrieverLegacy) Status() retriever.Status {
	m.StatusCalled = true
	if m.InitShouldFail {
		return retriever.RetrieverError
	}

	return retriever.RetrieverReady
}

// GetName returns the name of the mock retriever.
func (m *InitializableRetrieverLegacy) GetName() string {
	return m.Name
}

// compile-time check of the interface implemented by the type.
var _ retriever.InitializableRetrieverLegacy = (*InitializableRetrieverLegacy)(nil)
