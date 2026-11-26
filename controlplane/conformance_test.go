package controlplane_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const vectors = "testdata/v1"

// check decodes and validates one vector the way the relay does and returns the unknown fields and the first
// error. Flag-set vectors resolve their sources against valid/manifest-basic.yaml.
func check(t *testing.T, path string) ([]string, error) {
	t.Helper()

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	base := filepath.Base(path)
	format := strings.TrimPrefix(filepath.Ext(base), ".")

	switch {
	case strings.HasPrefix(base, "manifest-"):
		manifest, info, decodeErr := controlplane.UnmarshalManifest(b)
		if decodeErr != nil {
			return nil, decodeErr
		}

		return info.UnknownFields, controlplane.ValidateManifest(manifest)
	case strings.HasPrefix(base, "flagset-"):
		doc, info, decodeErr := controlplane.UnmarshalFlagSet(b)
		if decodeErr != nil {
			return nil, decodeErr
		}

		raw, readErr := os.ReadFile(filepath.Join(vectors, "valid", "manifest-basic.yaml"))
		require.NoError(t, readErr)

		manifest, _, manifestErr := controlplane.UnmarshalManifest(raw)
		require.NoError(t, manifestErr)

		return info.UnknownFields, controlplane.ValidateFlagSet(doc, manifest)
	default:
		flags, info, decodeErr := controlplane.UnmarshalFlags(b, format)
		if decodeErr != nil {
			return nil, decodeErr
		}

		return info.UnknownFields, controlplane.ValidateFlags(flags)
	}
}

func vectorFiles(t *testing.T, dir string) []string {
	t.Helper()

	all, err := filepath.Glob(filepath.Join(vectors, dir, "*"))
	require.NoError(t, err)

	var files []string

	for _, file := range all {
		if ext := filepath.Ext(file); ext == ".yaml" || ext == extJSON {
			files = append(files, file)
		}
	}

	require.NotEmpty(t, files)

	return files
}

func TestConformanceValid(t *testing.T) {
	t.Parallel()

	for _, file := range vectorFiles(t, "valid") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			unknown, err := check(t, file)
			require.NoError(t, err)
			assert.Empty(t, unknown)
		})
	}
}

func TestConformanceInvalid(t *testing.T) {
	t.Parallel()

	sentinels := map[string]error{
		"ErrWrongKind":             controlplane.ErrWrongKind,
		"ErrUnsupportedAPIVersion": controlplane.ErrUnsupportedAPIVersion,
		"ErrInvalid":               controlplane.ErrInvalid,
		"ErrDecode":                controlplane.ErrDecode,
	}

	for _, file := range vectorFiles(t, "invalid") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			side, err := os.ReadFile(file + ".error")
			require.NoError(t, err)

			lines := strings.Split(strings.TrimSpace(string(side)), "\n")
			require.Len(t, lines, 2)

			want, ok := sentinels[lines[0]]
			require.True(t, ok, "unknown error class %q", lines[0])

			_, err = check(t, file)
			require.ErrorIs(t, err, want)
			assert.Contains(t, err.Error(), lines[1])
		})
	}
}

func TestConformanceForwardCompatible(t *testing.T) {
	t.Parallel()

	for _, file := range vectorFiles(t, "forward-compatible") {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()

			side, err := os.ReadFile(file + ".unknown")
			require.NoError(t, err)

			unknown, err := check(t, file)
			require.NoError(t, err)
			assert.Equal(t, strings.Fields(string(side)), unknown)
		})
	}
}
