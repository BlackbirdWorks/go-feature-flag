package bootstrap_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

const (
	controlConnPath = "controlPlane" + ".connection"
	accessID        = "access-id"
)

const fileControlDoc = `version: 2
connections:
  disk: {type: file, path: /srv/control}
controlPlane: {connection: disk, manifest: {key: m.json}}
`

func TestFileConnectionAndControlPlane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		doc      string
		wantErr  error
		wantPath string
	}{
		{name: "file control plane", doc: fileControlDoc},
		{
			name:    "file without a path",
			doc:     "version: 2\nconnections:\n  disk: {type: file}\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "connections.disk.path",
		},
		{
			name:    "file with an s3 key",
			doc:     "version: 2\nconnections:\n  disk: {type: file, path: /x, bucket: b}\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "connections.disk.bucket",
		},
		{
			name: "control plane on a kinesis connection",
			doc: "version: 2\nconnections:\n  k: {type: kinesis, region: r}\n" +
				"controlPlane: {connection: k, manifest: {key: m.json}}\n",
			wantErr: bootstrap.ErrWrongConnectionType, wantPath: controlConnPath,
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tcase.doc), nil, bootstrap.LoadOptions{})
			if tcase.wantErr == nil {
				require.NoError(t, err)
				assert.Equal(t, bootstrap.TypeFile, cfg.Connections["disk"].Type())
				assert.Equal(t, "/srv/control", cfg.Connections["disk"].File.Path)

				return
			}

			require.ErrorIs(t, err, tcase.wantErr)

			var perr *bootstrap.Error

			require.ErrorAs(t, err, &perr)
			assert.Equal(t, tcase.wantPath, perr.Path)
		})
	}
}

func TestConnectionSpec(t *testing.T) {
	t.Parallel()

	const doc = `version: 2
connections:
  s3c:
    type: s3
    region: us-east-1
    bucket: b
    caBundleFile: /ca.pem
    pathStyle: true
    credentials:
      type: static
      accessKeyId: {fromEnv: AK}
      secretAccessKey: {fromEnv: SK}
    timeouts: {connect: 5s, request: 7s}
    retry: {maxAttempts: 4, maxBackoff: 9s}
  kin: {type: kinesis, region: us-west-2, stream: s}
  q: {type: sqs, region: us-west-2, queueUrl: "https://q.example/q"}
  wh: {type: redshift, region: us-west-2}
  disk: {type: file, path: /srv}
`

	opts := options(envMap{"AK": accessID, "SK": "secret-key"}, nil, false)
	cfg, err := bootstrap.Parse([]byte(doc), nil, opts)
	require.NoError(t, err)

	tests := []struct {
		name     string
		conn     string
		wantKind string
	}{
		{name: "s3", conn: "s3c", wantKind: connection.KindS3},
		{name: bootstrap.TypeKinesis, conn: "kin", wantKind: connection.KindKinesis},
		{name: bootstrap.TypeSQS, conn: "q", wantKind: connection.KindSQS},
		{name: bootstrap.TypeRedshift, conn: "wh", wantKind: connection.KindRedshift},
		{name: "file has no spec", conn: "disk", wantKind: ""},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			spec, specErr := bootstrap.ConnectionSpec(cfg.Connections[tcase.conn], opts)
			require.NoError(t, specErr)

			if tcase.wantKind == "" {
				assert.Nil(t, spec)

				return
			}

			assert.Equal(t, tcase.wantKind, spec.Kind())
			require.NoError(t, spec.Validate())
		})
	}

	spec, err := bootstrap.S3Spec(cfg.Connections["s3c"].S3, opts)
	require.NoError(t, err)
	assert.Equal(t, "/ca.pem", spec.CABundleFile)
	assert.Equal(t, accessID, spec.Credentials.AccessKeyID.Reveal())
	assert.Equal(t, 4, spec.Retry.MaxAttempts)
	assert.True(t, spec.PathStyle)
}

func TestConnectionSpecSecretErrorsNameTheSettingOnly(t *testing.T) {
	t.Parallel()

	const doc = `version: 2
connections:
  c:
    type: s3
    region: r
    credentials:
      type: %s
      %s
`

	tests := []struct {
		name     string
		body     string
		wantText string
	}{
		{
			name:     "access key",
			body:     "static\n      accessKeyId: {fromEnv: MISSING}\n      secretAccessKey: {fromEnv: SK}",
			wantText: "credentials.accessKeyId",
		},
		{
			name:     "secret key",
			body:     "static\n      accessKeyId: {fromEnv: AK}\n      secretAccessKey: {fromEnv: MISSING}",
			wantText: "credentials.secretAccessKey",
		},
		{
			name:     "external id",
			body:     "assumeRole\n      roleArn: arn:aws:iam::1:role/x\n      externalId: {fromEnv: MISSING}",
			wantText: "credentials.externalId",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			parts := splitType(tcase.body)
			opts := options(envMap{"AK": accessID, "SK": "topsecret"}, nil, false)

			cfg, err := bootstrap.Parse([]byte(fmt.Sprintf(doc, parts[0], parts[1])), nil, options(envMap{
				"AK": accessID, "SK": "topsecret", "MISSING": "x",
			}, nil, false))
			require.NoError(t, err)

			_, err = bootstrap.ConnectionSpec(cfg.Connections["c"], opts)
			require.ErrorIs(t, err, bootstrap.ErrSecretUnresolved)
			assert.Contains(t, err.Error(), tcase.wantText)
			assert.NotContains(t, err.Error(), "topsecret")
		})
	}
}

func splitType(body string) [2]string {
	typ, rest, _ := strings.Cut(body, "\n      ")

	return [2]string{typ, rest}
}
