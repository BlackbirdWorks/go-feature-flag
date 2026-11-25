package testutils

import (
	"errors"
	"fmt"
	"io"
	"os"
)

var (
	errFailedToReadFrom = errors.New("failed to read from GCP")
)

// GCStorageReaderMock is a mock of a Google Cloud Storage object reader.
type GCStorageReaderMock struct {
	FileToRead string
	data       []byte
	readIndex  int64
	ShouldFail bool
}

// Close does nothing and always returns nil.
func (*GCStorageReaderMock) Close() error {
	return nil
}

func (r *GCStorageReaderMock) Read(buf []byte) (int, error) {
	if r.ShouldFail {
		return 0, errFailedToReadFrom
	}

	// Set the mocked data to be read.
	if r.data == nil {
		data, err := os.ReadFile(r.FileToRead)
		if err != nil {
			return 0, fmt.Errorf("read mock GCS file %s: %w", r.FileToRead, err)
		}

		r.data = data
	}

	// Return io.EOF if read all bytes.
	if r.readIndex >= int64(len(r.data)) {
		return 0, io.EOF
	}

	// Copy unread bytes.
	n := copy(buf, r.data[r.readIndex:])
	r.readIndex += int64(n)

	return n, nil
}
