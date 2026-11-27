package publish_test

import (
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

// denyingTransport answers every request with 403 AccessDenied and records them (method, host and path).
type denyingTransport struct {
	seen     []string
	mu       sync.Mutex
	requests atomic.Int32
}

func (d *denyingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	d.requests.Add(1)

	d.mu.Lock()
	d.seen = append(d.seen, req.Method+" "+req.URL.Host+req.URL.Path)
	d.mu.Unlock()

	body := `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>denied</Message></Error>`

	return &http.Response{
		StatusCode: http.StatusForbidden, Status: "403 Forbidden", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{"Content-Type": []string{"application/xml"}},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: req,
	}, nil
}

// exactDir opens dir only: any other path is a directory that does not exist.
func exactDir(dir string, tree fs.FS) func(string) fs.FS {
	return func(path string) fs.FS {
		if path == dir {
			return tree
		}

		return missingDir{dir: path}
	}
}

const assumeRoleFile = `version: 2
connections:
  control:
    type: s3
    region: us-east-1
    bucket: acme
    credentials:
      type: assumeRole
      roleArn: arn:aws:iam::123456789012:role/publisher
      sessionName: cli
      externalId: {fromEnv: EXTERNAL_ID}
controlPlane:
  connection: control
  manifest: {key: manifests/stable.yaml}
`

const staticFile = `version: 2
connections:
  control:
    type: s3
    region: us-east-1
    bucket: acme
    credentials:
      type: static
      accessKeyId: {fromEnv: PUBLISH_KEY_ID}
      secretAccessKey: {fromEnv: PUBLISH_SECRET}
      sessionToken: {fromEnv: PUBLISH_TOKEN}
controlPlane:
  connection: control
  manifest: {key: manifests/stable.yaml}
`

func TestCredentialsOfTheBootstrapFileReachTheSpec(t *testing.T) {
	t.Parallel()

	rig := newHarness(t, sourceTree())
	rig.withFiles(
		map[string]string{"/role.yaml": assumeRoleFile, "/static.yaml": staticFile},
		map[string]string{
			"EXTERNAL_ID":    "ext-1",
			envKeyID:         canaryKeyID,
			"PUBLISH_SECRET": canarySecret,
			"PUBLISH_TOKEN":  "tok-1",
		},
	)

	_, _, err := rig.run(t, srcDir, optConfig, "/role.yaml", "--dry-run")
	require.NoError(t, err)

	_, _, err = rig.run(t, srcDir, optConfig, "/static.yaml", "--dry-run")
	require.NoError(t, err)
	require.Len(t, rig.specs, 2)

	role := rig.specs[0].Credentials
	assert.Equal(t, connection.CredentialsAssumeRole, role.Type)
	assert.Equal(t, "arn:aws:iam::123456789012:role/publisher", role.RoleARN)
	assert.Equal(t, "cli", role.SessionName)
	assert.Equal(t, "ext-1", role.ExternalID)

	static := rig.specs[1].Credentials
	assert.Equal(t, connection.CredentialsStatic, static.Type)
	assert.Equal(t, canaryKeyID, static.AccessKeyID.Reveal())
	assert.Equal(t, canarySecret, static.SecretAccessKey.Reveal())
	assert.Equal(t, "tok-1", static.SessionToken.Reveal())
}

func TestPublishThroughTheConnectionFactory(t *testing.T) {
	t.Parallel()

	transport := &denyingTransport{}
	deps := publish.DefaultDeps()
	deps.OpenDir = exactDir(srcDir, sourceTree())
	deps.ConnectionOptions = []connection.Option{connection.WithRoundTripper(transport)}
	deps.ReadFile = func(name string) ([]byte, error) {
		if name != "/b.yaml" {
			return nil, &fs.PathError{Op: opOpen, Path: name, Err: fs.ErrNotExist}
		}

		return []byte(staticFile), nil
	}
	deps.LookupEnv = func(name string) (string, bool) {
		value, ok := map[string]string{
			envKeyID: canaryKeyID, "PUBLISH_SECRET": canarySecret, "PUBLISH_TOKEN": "t",
		}[name]

		return value, ok
	}

	var stdout, stderr bytes.Buffer

	err := publish.Run(
		t.Context(),
		[]string{srcDir, optConfig, "/b.yaml"},
		&helper.Streams{Stdout: &stdout, Stderr: &stderr},
		deps,
	)
	require.Error(t, err)

	assert.Equal(t, helper.ExitTransport, helper.ExitCodeOf(err), "an access error of the store is a transport failure")
	assert.Contains(t, err.Error(), "AccessDenied")
	assert.NotContains(t, err.Error(), canarySecret)
	assert.Positive(t, transport.requests.Load(), "the request went through the factory's client")

	transport.mu.Lock()
	defer transport.mu.Unlock()

	require.NotEmpty(t, transport.seen)
	assert.Equal(t, "GET acme.s3.us-east-1.amazonaws.com/manifests/stable.yaml", transport.seen[0],
		"the first request reads the manifest of the connection's bucket and region")
}

func TestPublishFactoryRejectsAnInvalidSpec(t *testing.T) {
	t.Parallel()

	deps := publish.DefaultDeps()
	deps.OpenDir = exactDir(srcDir, sourceTree())

	var stdout, stderr bytes.Buffer

	err := publish.Run(
		t.Context(),
		[]string{srcDir, optBucket, "b", optRegion, regionUSEast, optEndpoint, "://bad"},
		&helper.Streams{Stdout: &stdout, Stderr: &stderr},
		deps,
	)
	require.Error(t, err)
	assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err), err.Error())
}

func TestNewPublishCmdIsWiredToTheRealEnvironment(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	err := publish.Run(
		t.Context(),
		[]string{"--help"},
		&helper.Streams{Stdout: &stdout, Stderr: &stderr},
		publish.DefaultDeps(),
	)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), verifyWord)

	deps := publish.DefaultDeps()
	assert.Equal(
		t,
		"2025-06-15T12:00:00Z",
		deps.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
		"the clock of the repository",
	)
}

const pollFile = "/poll.yaml"

// The polling bounds are a check of the relay: publish and verify read only the destination of the file.
func TestPublishAndVerifyIgnoreTheReaderPollingBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setting string
		verify  bool
	}{
		{name: "publish, sub-second interval", setting: "pollInterval: 500ms"},
		{name: "publish, jitter of one", setting: "jitter: 1"},
		{name: "verify, sub-second interval", setting: "pollInterval: 500ms", verify: true},
		{name: "verify, jitter of one", setting: "jitter: 1", verify: true},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			file := "version: 2\nconnections:\n  control: {type: s3, region: us-east-1, bucket: acme}\n" +
				"controlPlane:\n  connection: control\n  manifest: {key: manifests/stable.yaml, " + tcase.setting + "}\n"
			rig := newHarness(t, sourceTree())
			rig.withFiles(map[string]string{pollFile: file}, nil)

			args := []string{srcDir, optConfig, pollFile, "--dry-run"}
			if tcase.verify {
				args = []string{verifyWord, optConfig, pollFile}
			}

			_, _, err := rig.run(t, args...)
			require.NotErrorIs(t, err, bootstrap.ErrInvalidValue)
			require.Len(t, rig.specs, 1, "the destination was resolved and the factory asked for a client")
		})
	}
}
