package mockretriever

import (
	"context"
	"fmt"
	"os"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// FileInitializableRetriever is the existing file-based mock that creates/deletes files
// This maintains compatibility with existing tests.
type FileInitializableRetriever struct {
	Path          string
	CurrentStatus retriever.Status
}

func NewFileInitializableRetriever(path string, status retriever.Status) *FileInitializableRetriever {
	return &FileInitializableRetriever{
		Path:          path,
		CurrentStatus: status,
	}
}

// Retrieve reads the file and returns the content.
func (r *FileInitializableRetriever) Retrieve(_ context.Context) ([]byte, error) {
	content, err := os.ReadFile(r.Path)
	if err != nil {
		return nil, fmt.Errorf("read mock retriever file %s: %w", r.Path, err)
	}

	return content, nil
}

func (r *FileInitializableRetriever) Init(_ context.Context, _ *fflog.FFLogger) error {
	yamlString := `flag-xxxx-123:
  variations:
    A: true
    B: false
  defaultRule:
    variation: A`

	yamlBytes := []byte(yamlString)

	err := os.WriteFile(r.Path, yamlBytes, 0600)
	if err != nil {
		return fmt.Errorf("write mock retriever file %s: %w", r.Path, err)
	}

	return nil
}

func (r *FileInitializableRetriever) Shutdown(_ context.Context) error {
	err := os.Remove(r.Path)
	if err != nil {
		return fmt.Errorf("remove mock retriever file %s: %w", r.Path, err)
	}

	return nil
}

func (r *FileInitializableRetriever) Status() retriever.Status {
	return r.CurrentStatus
}
