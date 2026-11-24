package testutils

import (
	"crypto/md5" //nolint:gosec // md5 is the checksum the GCS API reports, not a security use
	"encoding/base64"
	"os"
	"testing"

	"github.com/fsouza/fake-gcs-server/fakestorage"
	"github.com/stretchr/testify/require"
)

type MockedStorage struct {
	Server *fakestorage.Server
}

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

func EncodedMd5Hash(content []byte) string {
	h := md5.New() //nolint:gosec // md5 is the checksum the GCS API reports, not a security use
	h.Write(content)

	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
