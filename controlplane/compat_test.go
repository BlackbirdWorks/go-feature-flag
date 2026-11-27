package controlplane_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
			wantFlagsV1(),
		},
		{
			"flag file json", "flagfile.json", readFlags(controlplane.FormatJSON, controlplane.MarshalFlagsJSON),
			wantFlagsV1(),
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

// The names of the frozen documents, spelled as the files spell them.
const (
	frozenKindManifest = "Manifest"
	frozenSource       = "checkout-flags"
	frozenYAML         = "yaml"
	frozenSetCheckout  = "checkout"
	frozenSetEmpty     = "no-keys-yet"
	frozenFlagCheckout = "new-checkout"
	frozenOff          = "off"
)

// The expectations below are literals, on purpose: they must not move with the live fixtures or constants.

func frozenTime(day int) *time.Time {
	t := time.Date(2026, time.January, day, 3, 4, 5, 0, time.UTC)

	return &t
}

// wantManifestV1 is the manifest that manifest.yaml and manifest.json hold.
func wantManifestV1() *controlplane.Manifest {
	return &controlplane.Manifest{
		APIVersion: "semaphore/v1",
		Kind:       frozenKindManifest,
		Channel:    "stable",
		Generation: 7,
		CreatedAt:  frozenTime(2),
		Objects: []controlplane.ManifestObject{
			{
				Name:       frozenSource,
				Kind:       "flags",
				Connection: "flags-eu",
				Key:        "objects/sha256/29/2934fb49d6b38adef036c28fac69f188feae09259f3184e6d9cfbd0f35454fec.yaml",
				Format:     frozenYAML,
				Checksum: controlplane.Checksum{
					Algorithm: "sha256",
					Value:     "2934fb49d6b38adef036c28fac69f188feae09259f3184e6d9cfbd0f35454fec",
				},
				Size:      5,
				VersionID: "v1",
			},
			{
				Name:       "flagsets",
				Kind:       "flagSets",
				Connection: "control",
				Version:    1,
				Key:        "objects/sha256/06/069100af988b1eda7efb84e2964ebff7a05592a3aec9124a576d83a340fe1e0e.yaml",
				Format:     frozenYAML,
				Checksum: controlplane.Checksum{
					Algorithm: "sha256",
					Value:     "069100af988b1eda7efb84e2964ebff7a05592a3aec9124a576d83a340fe1e0e",
				},
				Size: 9,
			},
		},
	}
}

// wantFlagSetV1 is the flag-set document that flagset.yaml and flagset.json hold.
func wantFlagSetV1() *controlplane.FlagSetDocument {
	return &controlplane.FlagSetDocument{
		APIVersion: "semaphore/v1",
		Kind:       "FlagSet",
		FlagSets: []controlplane.FlagSet{
			{
				Name:    frozenSetCheckout,
				Sources: []string{frozenSource},
				APIKeys: []string{"6f1f1b7e-6a0e-4d4c-9d8e-2f6f3f1a0b11"},
			},
			{Name: frozenSetEmpty, Sources: []string{frozenSource}},
		},
	}
}

// wantFlagsV1 is the flag file that flagfile.yaml and flagfile.json hold.
func wantFlagsV1() controlplane.Flags {
	return controlplane.Flags{
		frozenFlagCheckout: {
			TrackEvents: new(false),
			Disable:     new(false),
			Version:     new("1.2"),
			Variations:  map[string]any{"on": true, frozenOff: false},
			Targeting: []controlplane.Rule{
				{
					Name: new("beta"), Query: new("beta eq true"), OnError: new("match"),
					Percentage: map[string]float64{"on": 100, frozenOff: 0},
				},
				{
					Name:  new("ramp"),
					Query: new(`country eq "FR"`),
					ProgressiveRollout: &controlplane.ProgressiveRollout{
						Initial: &controlplane.ProgressiveRolloutStep{
							Variation:  new(frozenOff),
							Percentage: new(0.0),
							Date:       frozenTime(1),
						},
						End: &controlplane.ProgressiveRolloutStep{
							Variation:  new("on"),
							Percentage: new(100.0),
							Date:       frozenTime(2),
						},
					},
				},
			},
			BucketingKey: new("targetingKey"),
			DefaultRule:  &controlplane.Rule{Variation: new(frozenOff)},
			ScheduledRollout: []controlplane.ScheduledStep{{
				Date:        frozenTime(3),
				DefaultRule: &controlplane.Rule{Variation: new("on")},
				Metadata:    map[string]any{"step": "full"},
			}},
			Experimentation: &controlplane.Experimentation{Start: frozenTime(4), End: frozenTime(5)},
			Metadata:        map[string]any{"owner": "payments", "ratio": 1.5, "tags": []any{"yes", "007"}},
		},
		"banner": {
			Variations:  map[string]any{"a": "yes", "b": "no", "c": 0.5, "d": "2026-01-01"},
			DefaultRule: &controlplane.Rule{Variation: new("a")},
		},
	}
}
