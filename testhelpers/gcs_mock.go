package testhelpers

import (
	"crypto/md5" //nolint:gosec // md5 is the checksum the GCS API reports, not a security use
	"encoding/base64"
	"os"
	"testing"

	"github.com/fsouza/fake-gcs-server/fakestorage"
	"github.com/stretchr/testify/require"
)

// MockedStorage is a fake Google Cloud Storage server used in tests.
type MockedStorage struct {
	Server *fakestorage.Server
}

// NewMockedGCS starts a fake Google Cloud Storage server for the test.
func NewMockedGCS(t *testing.T) MockedStorage {
	t.Helper()

	server := fakestorage.NewServer(nil)

	t.Cleanup(func() {
		server.Stop()
	})

	return MockedStorage{
		Server: server,
	}
}

// WithFiles adds the files to the bucket bucketName of the fake server.
func (m MockedStorage) WithFiles(t *testing.T, bucketName string, files map[string]string) {
	t.Helper()

	for filename, name := range files {
		content, err := os.ReadFile(filename)
		require.NoError(t, err, "could not read testfile")

		object := fakestorage.Object{
			Content:    content,
			BucketName: bucketName,
			Name:       name,
			Md5Hash:    EncodedMd5Hash(content),
		}
		m.Server.CreateObject(object)
	}
}

// EncodedMd5Hash returns the base64-encoded MD5 hash of content.
func EncodedMd5Hash(content []byte) string {
	h := md5.New() //nolint:gosec // md5 is the checksum the GCS API reports, not a security use
	h.Write(content)

	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
