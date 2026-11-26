package publish_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

const sqsFile = `version: 2
connections:
  control: {type: sqs, region: us-east-1}
controlPlane:
  connection: control
  manifest: {key: m.yaml}
`

const bootstrapFile = `version: 2
connections:
  control:
    type: s3
    region: eu-west-1
    bucket: acme-control
    endpoint: http://localhost:4566
    pathStyle: true
    requestChecksumCalculation: whenRequired
    timeouts: {connect: 5s, request: 20s}
    retry: {maxAttempts: 4, maxBackoff: 30s}
    credentials:
      type: static
      accessKeyId: {fromEnv: PUBLISH_KEY_ID}
      secretAccessKey: {fromFile: /run/secret-key}
controlPlane:
  connection: control
  manifest:
    key: manifests/prod/stable.json
    channel: stable
    maxObjects: 1
`

func (rig *harness) withFiles(files, env map[string]string) {
	rig.deps.ReadFile = func(name string) ([]byte, error) {
		if data, ok := files[name]; ok {
			return []byte(data), nil
		}

		return nil, &fs.PathError{Op: opOpen, Path: name, Err: fs.ErrNotExist}
	}
	rig.deps.LookupEnv = func(name string) (string, bool) {
		v, ok := env[name]

		return v, ok
	}
}

func TestPublishFromBootstrapFile(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, treeOf(map[string]string{flagsA: checkoutFlags}))
	rig.fake.Bucket = "acme-control" // the bucket of the connection in the bootstrap file
	rig.withFiles(
		map[string]string{bootstrapPath: bootstrapFile, secretKeyPath: canarySecret + "\n"},
		map[string]string{envKeyID: canaryKeyID},
	)

	stdout, stderr, err := rig.run(t, srcDir, optConfig, bootstrapPath, "--dry-run")
	require.NoError(t, err)

	for _, text := range []string{stdout, stderr} {
		assert.NotContains(t, text, canarySecret)
		assert.NotContains(t, text, canaryKeyID)
		assert.NotContains(t, text, canaryAPIKey)
	}

	require.Len(t, rig.specs, 1)

	spec := rig.specs[0]
	assert.Equal(t, "control", rig.names[0])
	assert.Equal(t, "eu-west-1", spec.Region)
	assert.Equal(t, "http://localhost:4566", spec.Endpoint)
	assert.Equal(t, "acme-control", spec.Bucket)
	assert.True(t, spec.PathStyle)
	assert.Equal(t, connection.ChecksumWhenRequired, spec.RequestChecksumCalculation)
	assert.Equal(t, connection.CredentialsStatic, spec.Credentials.Type)
	assert.Equal(t, canaryKeyID, spec.Credentials.AccessKeyID.Reveal())
	assert.Equal(t, canarySecret, spec.Credentials.SecretAccessKey.Reveal(), "the file secret is read and trimmed")
	assert.Equal(t, 4, spec.Retry.MaxAttempts)
	assert.Equal(t, "30s", spec.Retry.MaxBackoff.String())
	assert.Equal(t, "5s", spec.ConnectTimeout.String())
	assert.Equal(t, "20s", spec.RequestTimeout.String())
}

func TestTheObjectLimitOfTheBootstrapFileStopsTheLoading(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree()) // two documents; the file says maxObjects: 1
	rig.withFiles(
		map[string]string{bootstrapPath: bootstrapFile, secretKeyPath: canarySecret},
		map[string]string{envKeyID: canaryKeyID},
	)

	stdout, stderr, err := rig.run(t, srcDir, optConfig, bootstrapPath)
	require.Error(t, err)
	assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err))
	assert.Contains(t, err.Error(), "more than 1 files")
	assert.Empty(t, rig.fake.Calls())

	for _, text := range []string{stdout, stderr, err.Error()} {
		assert.NotContains(t, text, canarySecret)
		assert.NotContains(t, text, canaryKeyID)
	}
}

func TestPublishFromBootstrapFileWritesToTheConfiguredManifestKey(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, treeOf(map[string]string{flagsA: checkoutFlags}))
	rig.fake.Bucket = "acme-control" // the bucket of the connection in the bootstrap file
	rig.withFiles(
		map[string]string{bootstrapPath: bootstrapFile, secretKeyPath: canarySecret},
		map[string]string{envKeyID: canaryKeyID},
	)

	stdout, _, err := rig.run(t, srcDir, optConfig, bootstrapPath, "--prefix", "acme")
	require.NoError(t, err)

	assert.Contains(t, stdout, "published generation 1 (was 0) to manifests/prod/stable.json, channel stable")

	body, ok := rig.fake.Body("manifests/prod/stable.json")
	require.True(t, ok)
	assert.Equal(t, byte('{'), body[0], "a .json manifest key is written as JSON")
	assert.Contains(t, string(body), `"connection": "control"`)
	assert.Contains(t, string(body), `"key": "acme/objects/sha256/`)
}

func TestPublishConfigErrors(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		cfgOK:         bootstrapFile,
		cfgV1:         "port: 1031\n",
		cfgUnknown:    "version: 2\nbogus: 1\n",
		"/no-cp.yaml": "version: 2\nconnections:\n  control: {type: s3, region: us-east-1, bucket: b}\n",
	}
	env := map[string]string{} // PUBLISH_KEY_ID is not set

	tests := []struct {
		name      string
		args      []string
		wantInErr []string
		wantCode  int
	}{
		{
			name:      "missing file",
			args:      []string{optConfig, "/nope.yaml"},
			wantCode:  helper.ExitFailure,
			wantInErr: []string{"/nope.yaml"},
		},
		{
			name:      "version 1 file",
			args:      []string{optConfig, cfgV1},
			wantCode:  helper.ExitInvalid,
			wantInErr: []string{cfgV1, "version 2 required"},
		},
		{
			name:      "unknown key with file path and line",
			args:      []string{optConfig, cfgUnknown},
			wantCode:  helper.ExitInvalid,
			wantInErr: []string{cfgUnknown, "bogus", "(line 2)"},
		},
		{
			name:      "no controlPlane",
			args:      []string{optConfig, "/no-cp.yaml"},
			wantCode:  helper.ExitInvalid,
			wantInErr: []string{"no controlPlane section"},
		},
		{
			name:      "unresolved secret",
			args:      []string{optConfig, cfgOK},
			wantCode:  helper.ExitInvalid,
			wantInErr: []string{envKeyID},
		},
		{
			name:      "flags that duplicate the file",
			args:      []string{optConfig, cfgOK, optBucket, "x", optChannel, "canary"},
			wantCode:  helper.ExitFailure,
			wantInErr: []string{"--bucket, --channel", "cannot be combined with --config"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newHarness(t, sourceTree())
			rig.withFiles(files, env)

			_, _, err := rig.run(t, append([]string{srcDir}, tt.args...)...)
			require.Error(t, err)
			assert.Equal(t, tt.wantCode, helper.ExitCodeOf(err), err.Error())

			for _, want := range tt.wantInErr {
				assert.Contains(t, err.Error(), want)
			}

			assert.Empty(t, rig.fake.Keys())
			assert.Empty(t, rig.specs, "no client was built")
		})
	}
}

func TestPublishWrongConnectionType(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())
	rig.withFiles(
		map[string]string{
			"/sqs.yaml": sqsFile,
		},
		nil,
	)

	_, _, err := rig.run(t, srcDir, optConfig, "/sqs.yaml")
	require.Error(t, err)
	assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err), err.Error())
	assert.Contains(t, err.Error(), "connection has the wrong type")
}

// TestExplicitFlagsReachTheClientTheManifestAndTheKeys: every flag that names a place is used, by what the fake can
// tell: the spec of the client (region, endpoint, bucket, connection name) and the keys that were written.
func TestExplicitFlagsReachTheClientTheManifestAndTheKeys(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())
	rig.fake = s3fake.New("custom/m.yaml")
	rig.fake.Bucket = "other-bucket"

	stdout, _, err := rig.run(
		t,
		srcDir,
		optBucket,
		"other-bucket",
		optRegion,
		"eu-west-1",
		optEndpoint,
		"http://localhost:4566",
		"--prefix",
		"pfx",
		optChannel,
		"canary",
		"--connection",
		"flags-eu",
		"--manifest-key",
		"custom/m.yaml",
	)
	require.NoError(t, err)
	assert.Contains(t, stdout, "to custom/m.yaml, channel canary")

	require.Len(t, rig.specs, 1)
	assert.Equal(t, "eu-west-1", rig.specs[0].Region)
	assert.Equal(t, "http://localhost:4566", rig.specs[0].Endpoint)
	assert.Equal(t, "other-bucket", rig.specs[0].Bucket)
	assert.True(t, rig.specs[0].PathStyle, "a custom endpoint is addressed path-style")
	assert.Equal(t, []string{"flags-eu"}, rig.names)

	body, ok := rig.fake.Body("custom/m.yaml")
	require.True(t, ok)

	manifest, _, err := controlplane.UnmarshalManifest(body)
	require.NoError(t, err)
	assert.Equal(t, "canary", manifest.Channel)

	for i := range manifest.Objects {
		assert.Equal(t, "flags-eu", manifest.Objects[i].Connection)
		assert.True(t, strings.HasPrefix(manifest.Objects[i].Key, "pfx/objects/sha256/"), manifest.Objects[i].Key)
	}

	// verify with the same place reads the same manifest.
	rig.fake.ResetCalls()

	_, _, err = rig.run(
		t,
		verifyWord,
		optBucket,
		"other-bucket",
		optRegion,
		"eu-west-1",
		"--prefix",
		"pfx",
		optChannel,
		"canary",
		"--connection",
		"flags-eu",
		"--manifest-key",
		"custom/m.yaml",
	)
	require.NoError(t, err)
	assert.Equal(t, "GET custom/m.yaml", rig.fake.Calls()[0])
}
