package testutils

import (
	"errors"
	"io"
	"os"
)

type GCStorageReaderMock struct {
	ShouldFail bool
	FileToRead string
	data       []byte
	readIndex  int64
}

func (r *GCStorageReaderMock) Close() error {
	return nil
}

func (r *GCStorageReaderMock) Read(p []byte) (n int, err error) {
	if r.ShouldFail {
		return 0, errors.New("failed to read from GCP")
	}

	// Set the mocked data to be read.
	if r.data == nil {
		r.data, err = os.ReadFile(r.FileToRead)
		if err != nil {
			return 0, err
		}
	}

	// Return io.EOF if read all bytes.
	if r.readIndex >= int64(len(r.data)) {
		err = io.EOF
		return n, err
	}

	// Copy unread bytes.
	n = copy(p, r.data[r.readIndex:])
	r.readIndex += int64(n)

	return n, err
}
