package publish_test

import (
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

// denyingTransport answers every request with 403 AccessDenied and counts them.
type denyingTransport struct{ requests atomic.Int32 }

func (d *denyingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	d.requests.Add(1)

	body := `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>denied</Message></Error>`

	return &http.Response{
		StatusCode: http.StatusForbidden, Status: "403 Forbidden", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{"Content-Type": []string{"application/xml"}},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: req,
	}, nil
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
	deps.OpenDir = func(string) fs.FS { return sourceTree() }
	deps.ConnectionOptions = []connection.Option{connection.WithRoundTripper(transport)}
	deps.ReadFile = func(string) ([]byte, error) { return []byte(staticFile), nil }
	deps.LookupEnv = func(name string) (string, bool) {
		return map[string]string{
			envKeyID: canaryKeyID, "PUBLISH_SECRET": canarySecret, "PUBLISH_TOKEN": "t",
		}[name], true
	}

	cmd := publish.NewPublishCmdWith(deps)
	cmd.SetArgs([]string{srcDir, optConfig, "/b.yaml"})

	err := cmd.ExecuteContext(t.Context())
	require.Error(t, err)

	assert.Equal(t, helper.ExitTransport, helper.ExitCodeOf(err), "an access error of the store is a transport failure")
	assert.Contains(t, err.Error(), "AccessDenied")
	assert.NotContains(t, err.Error(), canarySecret)
	assert.Positive(t, transport.requests.Load(), "the request went through the factory's client")
}

func TestPublishFactoryRejectsAnInvalidSpec(t *testing.T) {
	t.Parallel()

	deps := publish.DefaultDeps()
	deps.OpenDir = func(string) fs.FS { return sourceTree() }

	cmd := publish.NewPublishCmdWith(deps)
	cmd.SetArgs([]string{srcDir, optBucket, "b", optRegion, regionUSEast, "--endpoint", "://bad"})

	err := cmd.ExecuteContext(t.Context())
	require.Error(t, err)
	assert.Equal(t, helper.ExitInvalid, helper.ExitCodeOf(err), err.Error())
}

func TestNewPublishCmdIsWiredToTheRealEnvironment(t *testing.T) {
	t.Parallel()

	cmd := publish.NewPublishCmd()
	assert.Equal(t, "publish", strings.Fields(cmd.Use)[0])

	names := make([]string, 0, len(cmd.Commands()))
	for _, c := range cmd.Commands() {
		names = append(names, c.Name())
	}

	assert.Equal(t, []string{"verify"}, names)

	deps := publish.DefaultDeps()
	assert.Equal(
		t,
		"2025-06-15T12:00:00Z",
		deps.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
		"the clock of the repository",
	)
}
