package fileretriever

import (
	"context"
	"fmt"
	"os"
)

// Retriever is a configuration struct for a local flat file.
type Retriever struct {
	Path string
}

// Retrieve is reading the file and return the content.
func (r *Retriever) Retrieve(_ context.Context) ([]byte, error) {
	content, err := os.ReadFile(r.Path)
	if err != nil {
		return nil, fmt.Errorf("reading the file: %w", err)
	}

	return content, nil
}
