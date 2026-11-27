package relayapp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// docExampleMarker precedes the first yaml block of a documented full example.
const docExampleMarker = "<!-- full-example -->\n```yaml\n"

// The examples of the documentation that carry the full-example marker are bootstrap files: they load with the
// validation of the relay.
func TestDocumentedFullExamplesLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		page       string
		wantBucket string
	}{
		{page: "docs/integrations/retrievers/minio.md", wantBucket: "my-featureflag-bucket"},
	}

	secrets := map[string]string{"MINIO_ACCESS_KEY": "a", "MINIO_SECRET_KEY": "b", "SEMAPHORE_ADMIN_KEY": "c"}

	for _, tt := range tests {
		t.Run(tt.page, func(t *testing.T) {
			t.Parallel()

			page, err := os.ReadFile(filepath.Join("..", "..", "..", tt.page))
			require.NoError(t, err)

			_, after, found := strings.Cut(string(page), docExampleMarker)
			require.True(t, found, "the page has a full-example block")

			block, _, found := strings.Cut(after, "```")
			require.True(t, found)

			cfg, err := bootstrap.Parse([]byte(block), nil, bootstrap.LoadOptions{
				LookupEnv: func(name string) (string, bool) {
					v, ok := secrets[name]

					return v, ok
				},
			})
			require.NoError(t, err)

			conn := cfg.Connections["control"]
			require.NotNil(t, conn.S3)
			assert.Equal(t, tt.wantBucket, conn.S3.Bucket)
			assert.True(t, conn.S3.PathStyle)
		})
	}
}
