package mockretriever

import (
	"context"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// InitializableRetrieverWithFlagset implements the InitializableRetrieverWithFlagset interface.
type InitializableRetrieverWithFlagset struct {
	ReceivedLogger  *fflog.FFLogger
	ReceivedFlagset *string
	Name            string
	InitShouldFail  bool
	RetrieveCalled  bool
	InitCalled      bool
	ShutdownCalled  bool
	StatusCalled    bool
}

// NewInitializableRetrieverWithFlagset returns a mock retriever with the given name that takes a flagset.
func NewInitializableRetrieverWithFlagset(name string) *InitializableRetrieverWithFlagset {
	return &InitializableRetrieverWithFlagset{Name: name}
}

// Retrieve returns the mock flag configuration.
func (m *InitializableRetrieverWithFlagset) Retrieve(_ context.Context) ([]byte, error) {
	m.RetrieveCalled = true

	return []byte(defaultFlagConfig), nil
}

// Init initializes the mock retriever for the flagset.
func (m *InitializableRetrieverWithFlagset) Init(_ context.Context, logger *fflog.FFLogger, flagset *string) error {
	m.InitCalled = true
	m.ReceivedLogger = logger

	m.ReceivedFlagset = flagset
	if m.InitShouldFail {
		return errInitializationFailed
	}

	return nil
}

// Shutdown stops the mock retriever.
func (m *InitializableRetrieverWithFlagset) Shutdown(_ context.Context) error {
	m.ShutdownCalled = true

	return nil
}

// Status returns the current status of the mock retriever.
func (m *InitializableRetrieverWithFlagset) Status() retriever.Status {
	m.StatusCalled = true
	if m.InitShouldFail {
		return retriever.RetrieverError
	}

	return retriever.RetrieverReady
}

// GetName returns the name of the mock retriever.
func (m *InitializableRetrieverWithFlagset) GetName() string {
	return m.Name
}

// compile-time check of the interface implemented by the type.
var _ retriever.InitializableRetrieverWithFlagset = (*InitializableRetrieverWithFlagset)(nil)
