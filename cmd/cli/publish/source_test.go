package publish_test

import (
	"io/fs"
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
		limits      publish.SourceLimits
	}{
		{
			name: "valid tree, in path order, dotfiles ignored",
			tree: fstest.MapFS{
				"flags/b.json":       {Data: []byte("{}")},
				"flags/a.yml":        {Data: []byte("a")},
				"flagsets/set.yml":   {Data: []byte("s")},
				"segments/beta.json": {Data: []byte("{}")},
				"flags/.keep":        {},
				".git/config":        {},
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
				{
					Name:   "beta",
					Kind:   controlplane.KindSegments,
					Format: controlplane.FormatJSON,
					Source: "src/segments/beta.json",
					Body:   []byte("{}"),
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
			name: "a symbolic link is refused and never followed",
			tree: fstest.MapFS{
				flagsA:            {Data: []byte("a")},
				"flags/link.yaml": {Mode: fs.ModeSymlink, Data: []byte("a.yaml")},
			},
			wantErr:     publish.ErrNotRegular,
			wantErrText: `not a regular file "src/flags/link.yaml": L---------`,
		},
		{
			name:        "a symbolic link in place of a layout directory",
			tree:        fstest.MapFS{"flags": {Mode: fs.ModeSymlink, Data: []byte("elsewhere")}},
			wantErr:     publish.ErrNotRegular,
			wantErrText: `"src/flags"`,
		},
		{
			name:        "a named pipe is refused",
			tree:        fstest.MapFS{"flags/pipe.yaml": {Mode: fs.ModeNamedPipe}},
			wantErr:     publish.ErrNotRegular,
			wantErrText: `"src/flags/pipe.yaml"`,
		},
		{
			name:        "a device is refused",
			tree:        fstest.MapFS{"flags/dev.yaml": {Mode: fs.ModeDevice}},
			wantErr:     publish.ErrNotRegular,
			wantErrText: `"src/flags/dev.yaml"`,
		},
		{
			name:        "a socket is refused",
			tree:        fstest.MapFS{"flags/sock.yaml": {Mode: fs.ModeSocket}},
			wantErr:     publish.ErrNotRegular,
			wantErrText: `"src/flags/sock.yaml"`,
		},
		{
			name:        "a file over the limit is refused from its size",
			tree:        fstest.MapFS{flagsA: {Data: []byte("0123456789")}},
			limits:      publish.SourceLimits{MaxObjectBytes: 9},
			wantErr:     publish.ErrFileTooLarge,
			wantErrText: `"src/flags/a.yaml": 10 bytes, the limit is 9`,
		},
		{
			name:   "a file at the limit is read",
			tree:   fstest.MapFS{flagsA: {Data: []byte("012345678")}},
			limits: publish.SourceLimits{MaxObjectBytes: 9},
			wantObjects: []s3publish.Object{{
				Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML,
				Source: "src/flags/a.yaml", Body: []byte("012345678"),
			}},
		},
		{
			name: "more files than the limit stops the loading",
			tree: fstest.MapFS{
				flagsA:         {Data: []byte("a")},
				flagsB:         {Data: []byte("b")},
				"flags/c.yaml": {Data: []byte("c")},
			},
			limits:      publish.SourceLimits{MaxObjects: 2},
			wantErr:     publish.ErrSourceTooLarge,
			wantErrText: "more than 2 files",
		},
		{
			name:        "distinct documents over the total stop the loading",
			tree:        fstest.MapFS{flagsA: {Data: []byte("aaaa")}, flagsB: {Data: []byte("bbbb")}},
			limits:      publish.SourceLimits{MaxTotalBytes: 7},
			wantErr:     publish.ErrSourceTooLarge,
			wantErrText: `more than 7 bytes (at "src/flags/b.yaml")`,
		},
		{
			name: "equal documents count once in the total and share one copy",
			tree: fstest.MapFS{
				flagsA:         {Data: []byte("aaaa")},
				flagsB:         {Data: []byte("aaaa")},
				"flags/c.yaml": {Data: []byte("aaaa")},
			},
			limits: publish.SourceLimits{MaxTotalBytes: 4},
			wantObjects: []s3publish.Object{
				{
					Name:   "a",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "src/flags/a.yaml",
					Body:   []byte("aaaa"),
				},
				{
					Name:   "b",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "src/flags/b.yaml",
					Body:   []byte("aaaa"),
				},
				{
					Name:   "c",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Source: "src/flags/c.yaml",
					Body:   []byte("aaaa"),
				},
			},
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

			got, err := publish.LoadSource(tt.tree, srcDir, tt.limits)
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
