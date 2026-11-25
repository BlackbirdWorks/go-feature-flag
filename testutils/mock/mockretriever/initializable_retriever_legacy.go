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

func NewInitializableRetrieverLegacy(name string) *InitializableRetrieverLegacy {
	return &InitializableRetrieverLegacy{Name: name}
}

func (m *InitializableRetrieverLegacy) Retrieve(_ context.Context) ([]byte, error) {
	m.RetrieveCalled = true

	return []byte(defaultFlagConfig), nil
}

func (m *InitializableRetrieverLegacy) Init(_ context.Context, logger *log.Logger) error {
	m.InitCalled = true

	m.ReceivedLogger = logger
	if m.InitShouldFail {
		return errInitializationFailed
	}

	return nil
}

func (m *InitializableRetrieverLegacy) Shutdown(_ context.Context) error {
	m.ShutdownCalled = true

	return nil
}

func (m *InitializableRetrieverLegacy) Status() retriever.Status {
	m.StatusCalled = true
	if m.InitShouldFail {
		return retriever.RetrieverError
	}

	return retriever.RetrieverReady
}

func (m *InitializableRetrieverLegacy) GetName() string {
	return m.Name
}
