package controlplane_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// checkGolden compares got with testdata/golden/<name>. Set UPDATE_GOLDEN=1 to rewrite the files.
func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name)

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, got, 0o600))
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))
}

func TestMarshalGolden(t *testing.T) {
	t.Parallel()

	tests := []struct { //nolint:govet // fieldalignment: the columns of the table are in reading order
		name    string
		golden  string
		marshal func() ([]byte, error)
	}{
		{
			"manifest yaml",
			"manifest.yaml",
			func() ([]byte, error) { return controlplane.MarshalManifest(fixtureManifest()) },
		},
		{
			"manifest json",
			"manifest.json",
			func() ([]byte, error) { return controlplane.MarshalManifestJSON(fixtureManifest()) },
		},
		{
			"flagset yaml",
			"flagset.yaml",
			func() ([]byte, error) { return controlplane.MarshalFlagSet(fixtureFlagSet()) },
		},
		{
			"flagset json",
			"flagset.json",
			func() ([]byte, error) { return controlplane.MarshalFlagSetJSON(fixtureFlagSet()) },
		},
		{"flags yaml", "flags.yaml", func() ([]byte, error) { return controlplane.MarshalFlags(fixtureFlags()) }},
		{"flags json", "flags.json", func() ([]byte, error) { return controlplane.MarshalFlagsJSON(fixtureFlags()) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.marshal()
			require.NoError(t, err)
			checkGolden(t, tt.golden, got)

			again, err := tt.marshal()
			require.NoError(t, err)
			assert.Equal(t, string(got), string(again), "output is byte-identical on every run")
		})
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	wantManifest := fixtureManifest()
	wantManifest.APIVersion, wantManifest.Kind = controlplane.APIVersion, controlplane.DocumentKindManifest
	wantSet := fixtureFlagSet()
	wantSet.APIVersion, wantSet.Kind = controlplane.APIVersion, controlplane.DocumentKindFlagSet

	t.Run("manifest yaml", func(t *testing.T) {
		t.Parallel()
		roundTripManifest(t, controlplane.MarshalManifest, wantManifest)
	})
	t.Run("manifest json", func(t *testing.T) {
		t.Parallel()
		roundTripManifest(t, controlplane.MarshalManifestJSON, wantManifest)
	})
	t.Run("flagset yaml", func(t *testing.T) {
		t.Parallel()
		roundTripFlagSet(t, controlplane.MarshalFlagSet, wantSet)
	})
	t.Run("flagset json", func(t *testing.T) {
		t.Parallel()
		roundTripFlagSet(t, controlplane.MarshalFlagSetJSON, wantSet)
	})
	t.Run("flags yaml", func(t *testing.T) {
		t.Parallel()
		roundTripFlags(t, controlplane.MarshalFlags, controlplane.FormatYAML)
	})
	t.Run("flags json", func(t *testing.T) {
		t.Parallel()
		roundTripFlags(t, controlplane.MarshalFlagsJSON, controlplane.FormatJSON)
	})
}

func roundTripManifest(
	t *testing.T,
	marshal func(*controlplane.Manifest) ([]byte, error),
	want *controlplane.Manifest,
) {
	t.Helper()

	b, err := marshal(fixtureManifest())
	require.NoError(t, err)

	got, info, err := controlplane.UnmarshalManifest(b)
	require.NoError(t, err)
	assert.Empty(t, info.UnknownFields)
	assert.Equal(t, want, got)
}

func roundTripFlagSet(
	t *testing.T,
	marshal func(*controlplane.FlagSetDocument) ([]byte, error),
	want *controlplane.FlagSetDocument,
) {
	t.Helper()

	b, err := marshal(fixtureFlagSet())
	require.NoError(t, err)

	got, info, err := controlplane.UnmarshalFlagSet(b)
	require.NoError(t, err)
	assert.Empty(t, info.UnknownFields)
	assert.Equal(t, want, got)
}

func roundTripFlags(t *testing.T, marshal func(controlplane.Flags) ([]byte, error), format string) {
	t.Helper()

	b, err := marshal(fixtureFlags())
	require.NoError(t, err)

	got, info, err := controlplane.UnmarshalFlags(b, format)
	require.NoError(t, err)
	assert.Empty(t, info.UnknownFields)
	assert.Equal(t, fixtureFlags(), got)
}

func TestMarshalNilAndHeader(t *testing.T) {
	t.Parallel()

	_, errManifest := controlplane.MarshalManifest(nil)
	_, errManifestJSON := controlplane.MarshalManifestJSON(nil)
	_, errSet := controlplane.MarshalFlagSet(nil)
	_, errSetJSON := controlplane.MarshalFlagSetJSON(nil)
	_, errFlags := controlplane.MarshalFlags(nil)
	_, errFlagsJSON := controlplane.MarshalFlagsJSON(nil)
	_, errVersion := controlplane.MarshalManifest(&controlplane.Manifest{APIVersion: "semaphore/v9"})
	_, errKind := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{Kind: "Manifest"})

	tests := []struct { //nolint:govet // fieldalignment: the columns of the table are in reading order
		name    string
		err     error
		wantErr error
	}{
		{"nil manifest", errManifest, controlplane.ErrNilDocument},
		{"nil manifest json", errManifestJSON, controlplane.ErrNilDocument},
		{"nil flag set", errSet, controlplane.ErrNilDocument},
		{"nil flag set json", errSetJSON, controlplane.ErrNilDocument},
		{"nil flags", errFlags, controlplane.ErrNilDocument},
		{"nil flags json", errFlagsJSON, controlplane.ErrNilDocument},
		{"foreign apiVersion", errVersion, controlplane.ErrUnsupportedAPIVersion},
		{"foreign kind", errKind, controlplane.ErrWrongKind},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tt.err, tt.wantErr)
		})
	}
}
