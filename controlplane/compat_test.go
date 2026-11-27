package controlplane_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// TestCompatVersion1 reads the documents of testdata/compat/v1, which were written by the marshallers of format
// version 1 and are frozen: they are never regenerated (unlike testdata/golden). Every later version of this
// package must still parse them to the same values and write them back byte for byte. A change that needs a
// different byte here is a change of the wire format and needs a new directory, not an edit of this one.
func TestCompatVersion1(t *testing.T) {
	t.Parallel()

	tests := []struct { //nolint:govet // fieldalignment: the columns of the table are in reading order
		name string
		file string
		// read parses the document and returns the value and its re-marshalled bytes
		read      func(data []byte) (any, []byte, []string, error)
		wantValue any
	}{
		{
			"manifest yaml", "manifest.yaml", readManifest(controlplane.MarshalManifest), wantManifestV1(),
		},
		{
			"manifest json", "manifest.json", readManifest(controlplane.MarshalManifestJSON), wantManifestV1(),
		},
		{
			"flag set yaml", "flagset.yaml", readFlagSet(controlplane.MarshalFlagSet), wantFlagSetV1(),
		},
		{
			"flag set json", "flagset.json", readFlagSet(controlplane.MarshalFlagSetJSON), wantFlagSetV1(),
		},
		{
			"flag file yaml", "flagfile.yaml", readFlags(controlplane.FormatYAML, controlplane.MarshalFlags),
			fixtureFlags(),
		},
		{
			"flag file json", "flagfile.json", readFlags(controlplane.FormatJSON, controlplane.MarshalFlagsJSON),
			fixtureFlags(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := os.ReadFile(filepath.Join("testdata", "compat", "v1", tt.file))
			require.NoError(t, err)

			got, again, unknown, err := tt.read(data)
			require.NoError(t, err)

			assert.Empty(t, unknown, "a frozen document has no field the reader does not know")
			assert.Equal(t, tt.wantValue, got)
			assert.Equal(t, string(data), string(again), "re-marshalled bytes")
		})
	}
}

func readManifest(marshal func(*controlplane.Manifest) ([]byte, error)) func([]byte) (any, []byte, []string, error) {
	return func(data []byte) (any, []byte, []string, error) {
		doc, info, err := controlplane.UnmarshalManifest(data)
		if err != nil {
			return nil, nil, nil, err
		}

		out, err := marshal(doc)

		return doc, out, info.UnknownFields, err
	}
}

func readFlagSet(
	marshal func(*controlplane.FlagSetDocument) ([]byte, error),
) func([]byte) (any, []byte, []string, error) {
	return func(data []byte) (any, []byte, []string, error) {
		doc, info, err := controlplane.UnmarshalFlagSet(data)
		if err != nil {
			return nil, nil, nil, err
		}

		out, err := marshal(doc)

		return doc, out, info.UnknownFields, err
	}
}

func readFlags(
	format string,
	marshal func(controlplane.Flags) ([]byte, error),
) func([]byte) (any, []byte, []string, error) {
	return func(data []byte) (any, []byte, []string, error) {
		flags, info, err := controlplane.UnmarshalFlags(data, format)
		if err != nil {
			return nil, nil, nil, err
		}

		out, err := marshal(flags)

		return flags, out, info.UnknownFields, err
	}
}

// wantManifestV1 is the manifest that the frozen files hold: the fixture with the header the marshaller wrote.
func wantManifestV1() *controlplane.Manifest {
	m := fixtureManifest()
	m.APIVersion = controlplane.APIVersion
	m.Kind = controlplane.DocumentKindManifest

	return m
}

// wantFlagSetV1 is the flag-set document that the frozen files hold.
func wantFlagSetV1() *controlplane.FlagSetDocument {
	d := fixtureFlagSet()
	d.APIVersion = controlplane.APIVersion
	d.Kind = controlplane.DocumentKindFlagSet

	return d
}
