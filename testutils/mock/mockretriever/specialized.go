package mockretriever

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errFirstInitFailed = errors.New("first init failed")
)

// ContextAwareRetriever is a mock that respects context cancellation.
type ContextAwareRetriever struct{}

// NewContextAwareRetriever returns a mock retriever that honours context cancellation.
func NewContextAwareRetriever() *ContextAwareRetriever {
	return &ContextAwareRetriever{}
}

// Retrieve returns the mock flag configuration unless ctx is cancelled.
func (m *ContextAwareRetriever) Retrieve(ctx context.Context) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("retrieving with the mock retriever: %w", ctx.Err())
	default:
		return []byte(defaultFlagConfig), nil
	}
}

// Init initializes the mock retriever and honours context cancellation.
func (m *ContextAwareRetriever) Init(ctx context.Context, _ *fflog.FFLogger) error {
	select {
	case <-ctx.Done():
		return fmt.Errorf("initializing the mock retriever: %w", ctx.Err())
	default:
		return nil
	}
}

// Shutdown stops the mock retriever.
func (m *ContextAwareRetriever) Shutdown(_ context.Context) error {
	return nil
}

// Status returns the current status of the mock retriever.
func (m *ContextAwareRetriever) Status() retriever.Status {
	return retriever.RetrieverReady
}

// StatusChangingRetriever allows testing status changes during operations.
type StatusChangingRetriever struct {
	CurrentStatus retriever.Status
	StatusCalled  bool
}

// NewStatusChangingRetriever returns a mock retriever whose status changes during its lifecycle.
func NewStatusChangingRetriever() *StatusChangingRetriever {
	return &StatusChangingRetriever{}
}

// Retrieve returns the mock flag configuration.
func (m *StatusChangingRetriever) Retrieve(_ context.Context) ([]byte, error) {
	return []byte(defaultFlagConfig), nil
}

// Init initializes the mock retriever and changes its status.
func (m *StatusChangingRetriever) Init(_ context.Context, _ *fflog.FFLogger) error {
	m.CurrentStatus = retriever.RetrieverReady

	return nil
}

// Shutdown stops the mock retriever.
func (m *StatusChangingRetriever) Shutdown(_ context.Context) error {
	m.CurrentStatus = retriever.RetrieverNotReady

	return nil
}

// Status returns the current status of the mock retriever.
func (m *StatusChangingRetriever) Status() retriever.Status {
	m.StatusCalled = true
	if m.CurrentStatus == "" {
		return retriever.RetrieverNotReady
	}

	return m.CurrentStatus
}

// RecoverableRetriever can fail initially but succeed on retry.
type RecoverableRetriever struct {
	Name          string
	FailFirstInit bool
	InitCalled    bool
	InitCallCount int
}

// NewRecoverableRetriever returns a mock retriever that fails its first initialization and succeeds on retry.
func NewRecoverableRetriever(name string) *RecoverableRetriever {
	return &RecoverableRetriever{Name: name}
}

// Retrieve returns the mock flag configuration.
func (m *RecoverableRetriever) Retrieve(_ context.Context) ([]byte, error) {
	return []byte(defaultFlagConfig), nil
}

// Init initializes the mock retriever, failing on the first attempt.
func (m *RecoverableRetriever) Init(_ context.Context, _ *fflog.FFLogger) error {
	m.InitCalled = true
	m.InitCallCount++

	if m.FailFirstInit && m.InitCallCount == 1 {
		return errFirstInitFailed
	}

	return nil
}

// Shutdown stops the mock retriever.
func (m *RecoverableRetriever) Shutdown(_ context.Context) error {
	return nil
}

// Status returns the current status of the mock retriever.
func (m *RecoverableRetriever) Status() retriever.Status {
	if m.FailFirstInit && m.InitCallCount == 1 {
		return retriever.RetrieverError
	}

	return retriever.RetrieverReady
}

// GetName returns the name of the mock retriever.
func (m *RecoverableRetriever) GetName() string {
	return m.Name
}
