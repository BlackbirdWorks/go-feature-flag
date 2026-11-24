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

func NewSimpleRetriever(name string) *SimpleRetriever {
	return &SimpleRetriever{Name: name}
}

func (m *SimpleRetriever) Retrieve(_ context.Context) ([]byte, error) {
	m.RetrieveCalled = true
	if m.ShouldFail {
		return nil, errRetrieveFailed
	}

	return []byte(defaultFlagConfig), nil
}

func (m *SimpleRetriever) GetName() string {
	return m.Name
}
