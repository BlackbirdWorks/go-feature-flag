package publish_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

func TestLoadSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tree        fstest.MapFS
		wantErr     error
		name        string
		wantErrText string
		wantObjects []s3publish.Object
	}{
		{
			name: "valid tree, in path order, dotfiles ignored",
			tree: fstest.MapFS{
				"flags/b.json":     {Data: []byte("{}")},
				"flags/a.yml":      {Data: []byte("a")},
				"flagsets/set.yml": {Data: []byte("s")},
				"flags/.keep":      {},
				".git/config":      {},
			},
			wantObjects: []s3publish.Object{
				{
					Name:   "a",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "src/flags/a.yml",
					Body:   []byte("a"),
				},
				{
					Name:   "b",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatJSON,
					Source: "src/flags/b.json",
					Body:   []byte("{}"),
				},
				{
					Name:   "set",
					Kind:   controlplane.KindFlagSets,
					Format: controlplane.FormatYAML,
					Source: "src/flagsets/set.yml",
					Body:   []byte("s"),
				},
			},
		},
		{
			name:        "unknown file at the root",
			tree:        fstest.MapFS{flagsA: {Data: []byte("a")}, "README.md": {}},
			wantErr:     publish.ErrUnknownFile,
			wantErrText: `unknown file "src/README.md"`,
		},
		{
			name:        "unknown extension",
			tree:        fstest.MapFS{"flags/a.toml": {}},
			wantErr:     publish.ErrUnknownFile,
			wantErrText: `unknown file "src/flags/a.toml"`,
		},
		{
			name:        "unknown directory",
			tree:        fstest.MapFS{flagsA: {}, "other/a.yaml": {}},
			wantErr:     publish.ErrUnknownFile,
			wantErrText: `unknown file "src/other"`,
		},
		{
			name:        "nested directory",
			tree:        fstest.MapFS{"flags/sub/a.yaml": {}},
			wantErr:     publish.ErrUnknownFile,
			wantErrText: `unknown file "src/flags/sub"`,
		},
		{
			name:        "duplicate names across formats",
			tree:        fstest.MapFS{flagsA: {}, "flags/a.json": {}},
			wantErr:     publish.ErrDuplicateObject,
			wantErrText: `duplicate object name "a": src/flags/a.json and src/flags/a.yaml`,
		},
		{
			name:        "duplicate names across kinds",
			tree:        fstest.MapFS{flagsA: {}, "flagsets/a.yaml": {}},
			wantErr:     publish.ErrDuplicateObject,
			wantErrText: `duplicate object name "a"`,
		},
		{
			name:        "empty tree",
			tree:        fstest.MapFS{},
			wantErr:     publish.ErrNoDocuments,
			wantErrText: "no documents found in src",
		},
		{name: "only dotfiles", tree: fstest.MapFS{"flags/.keep": {}}, wantErr: publish.ErrNoDocuments},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := publish.LoadSource(tt.tree, srcDir)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Contains(t, err.Error(), tt.wantErrText)
				assert.Nil(t, got)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantObjects, got)
		})
	}
}
