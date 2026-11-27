package bootstrap_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

func loadExample(t *testing.T) *bootstrap.Config {
	t.Helper()

	data, err := os.ReadFile("testdata/bootstrap.yaml")
	require.NoError(t, err)

	cfg, err := bootstrap.Parse(data, nil, options(nil, nil, false))
	require.NoError(t, err)

	return cfg
}

func TestExampleEffectivePrintGolden(t *testing.T) {
	t.Parallel()

	cfg := loadExample(t)

	var got bytes.Buffer
	require.NoError(t, cfg.Print(&got))

	want, err := os.ReadFile("testdata/bootstrap.effective.golden.yaml")
	require.NoError(t, err)
	assert.Equal(t, string(want), got.String())
}

func TestExampleContent(t *testing.T) {
	t.Parallel()

	cfg := loadExample(t)

	assert.Len(t, cfg.Connections, 9)
	assert.Len(t, cfg.Exporters, 6)
	assert.Len(t, cfg.Notifiers, 2)
	assert.Equal(t, "control", cfg.ControlPlane.Connection)
	assert.Equal(t, bootstrap.TypeS3, cfg.Connections["dev-s3"].Type())
	assert.Equal(t, bootstrap.CredentialsStatic, cfg.Connections["dev-s3"].S3.Credentials.Type)
	assert.True(t, cfg.Connections["dev-s3"].S3.PathStyle)
}

func TestPrintIsIdempotent(t *testing.T) {
	t.Parallel()

	cfg := loadExample(t)

	var first bytes.Buffer
	require.NoError(t, cfg.Print(&first))

	// the printed form is itself a valid file, once the redaction markers are replaced by a reference
	again, err := bootstrap.Parse(bytes.ReplaceAll(first.Bytes(), []byte("'[REDACTED]'"), []byte("key")),
		nil, options(nil, nil, false))
	require.NoError(t, err)

	var second bytes.Buffer
	require.NoError(t, again.Print(&second))
	assert.Equal(t, first.String(), second.String())
}
