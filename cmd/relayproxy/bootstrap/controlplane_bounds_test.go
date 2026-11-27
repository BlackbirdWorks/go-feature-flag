package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const (
	boundsFile = "version: 2\nconnections:\n  disk: {type: file, path: /srv/control}\n"
	boundsS3   = "version: 2\nconnections:\n  s3c: {type: s3, region: r, bucket: b}\n"
	keyPath    = "controlPlane.manifest.key"
	pollPath   = "controlPlane.manifest.pollInterval"
)

func boundsPlane(conn, manifest string) string {
	return "controlPlane:\n  connection: " + conn + "\n  manifest: " + manifest + "\n"
}

// What bootstrap accepts for the control plane is what the manifest reader accepts, at both boundaries, and what
// the store of the connection can read.
func TestControlPlaneBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		doc      string
		wantErr  error
		wantPath string
	}{
		{name: "interval of one second", doc: boundsFile + boundsPlane("disk", "{key: m.json, pollInterval: 1s}")},
		{
			name:    "interval below one second",
			doc:     boundsFile + boundsPlane("disk", "{key: m.json, pollInterval: 999ms}"),
			wantErr: bootstrap.ErrInvalidValue, wantPath: pollPath,
		},
		{name: "jitter zero", doc: boundsFile + boundsPlane("disk", "{key: m.json, jitter: 0}")},
		{name: "jitter just below one", doc: boundsFile + boundsPlane("disk", "{key: m.json, jitter: 0.99}")},
		{
			name:    "jitter one",
			doc:     boundsFile + boundsPlane("disk", "{key: m.json, jitter: 1}"),
			wantErr: bootstrap.ErrInvalidValue, wantPath: "controlPlane.manifest.jitter",
		},
		{
			name:    "jitter negative",
			doc:     boundsFile + boundsPlane("disk", "{key: m.json, jitter: -0.1}"),
			wantErr: bootstrap.ErrInvalidValue, wantPath: "controlPlane.manifest.jitter",
		},
		{name: "file nested key", doc: boundsFile + boundsPlane("disk", "{key: manifests/prod/stable.yaml}")},
		{
			name:    "file key with a dot prefix",
			doc:     boundsFile + boundsPlane("disk", "{key: ./manifests/stable.yaml}"),
			wantErr: bootstrap.ErrInvalidValue, wantPath: keyPath,
		},
		{
			name:    "file key that leaves the directory",
			doc:     boundsFile + boundsPlane("disk", "{key: ../x}"),
			wantErr: bootstrap.ErrInvalidValue, wantPath: keyPath,
		},
		{
			name:    "file key that is absolute",
			doc:     boundsFile + boundsPlane("disk", "{key: /etc/m.json}"),
			wantErr: bootstrap.ErrInvalidValue, wantPath: keyPath,
		},
		{
			name:    "file key that is empty",
			doc:     boundsFile + boundsPlane("disk", "{key: \"\"}"),
			wantErr: bootstrap.ErrMissingValue, wantPath: keyPath,
		},
		{name: "s3 nested key", doc: boundsS3 + boundsPlane("s3c", "{key: manifests/prod/stable.yaml}")},
		{
			name:    "s3 key with a leading slash",
			doc:     boundsS3 + boundsPlane("s3c", "{key: /m.json}"),
			wantErr: bootstrap.ErrInvalidValue, wantPath: keyPath,
		},
		{
			name:    "s3 connection without a region",
			doc:     "version: 2\nconnections:\n  s3c: {type: s3, bucket: b}\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "connections.s3c.region",
		},
		{
			name:    "s3 connection with a blank region",
			doc:     "version: 2\nconnections:\n  s3c: {type: s3, region: \" \", bucket: b}\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "connections.s3c.region",
		},
		{
			name:    "file connection with a blank path",
			doc:     "version: 2\nconnections:\n  disk: {type: file, path: \" \"}\n",
			wantErr: bootstrap.ErrMissingValue, wantPath: "connections.disk.path",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(tcase.doc), nil, bootstrap.LoadOptions{})
			if tcase.wantErr == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, tcase.wantErr)

			var perr *bootstrap.Error

			require.ErrorAs(t, err, &perr)
			assert.Equal(t, tcase.wantPath, perr.Path)
		})
	}
}
