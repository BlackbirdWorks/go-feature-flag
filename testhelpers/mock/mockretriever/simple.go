package mockretriever

import (
	"context"
	"errors"
)

var (
	errRetrieveFailed = errors.New("retrieve failed")
)

// SimpleRetriever implements only the basic Retriever interface.
type SimpleRetriever struct {
	Name           string
	RetrieveCalled bool
	ShouldFail     bool
}

// NewSimpleRetriever returns a mock retriever with the given name.
func NewSimpleRetriever(name string) *SimpleRetriever {
	return &SimpleRetriever{Name: name}
}

// Retrieve returns the mock flag configuration.
func (m *SimpleRetriever) Retrieve(_ context.Context) ([]byte, error) {
	m.RetrieveCalled = true
	if m.ShouldFail {
		return nil, errRetrieveFailed
	}

	return []byte(defaultFlagConfig), nil
}

// GetName returns the name of the mock retriever.
func (m *SimpleRetriever) GetName() string {
	return m.Name
}
