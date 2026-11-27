package bootstrap_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The full example of the dynamic segments page is a bootstrap file that loads.
func TestDynamicSegmentsPageExampleLoads(t *testing.T) {
	t.Parallel()

	page, err := os.ReadFile("../../../docs/configure-flags/segments-dynamic.md")
	require.NoError(t, err)

	_, after, found := strings.Cut(string(page), "<!-- full-example -->\n```yaml\n")
	require.True(t, found, "the page has a full-example block")

	block, _, found := strings.Cut(after, "```")
	require.True(t, found)

	cfg, err := bootstrap.Parse([]byte(block), nil, options(envMap{"VALKEY_PASSWORD": "x"}, nil, false))
	require.NoError(t, err)

	conn := cfg.Connections["segments-valkey"]
	require.NotNil(t, conn.Valkey)
	assert.Equal(t, bootstrap.PipeliningAuto, conn.Valkey.Pipelining)
	assert.True(t, conn.Valkey.TLS.Enable)

	assert.Equal(t, bootstrap.Duration(2*time.Second), *cfg.Segments.Resolver.CallTimeout)
	assert.Equal(t, bootstrap.Duration(30*time.Second), *cfg.Segments.Resolver.Default.TTL)
}
