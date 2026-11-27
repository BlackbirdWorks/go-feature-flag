package controlplane_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	sourceNope = "nope"
	setTwo     = "two"
)

const canaryKeyAcrossDocs = "canary-cross-document-key-77"

func docOf(sets ...controlplane.FlagSet) *controlplane.FlagSetDocument {
	return &controlplane.FlagSetDocument{FlagSets: sets}
}

func flagSetOf(name, key string) controlplane.FlagSet {
	set := controlplane.FlagSet{Name: name, Sources: []string{nameCheckoutFlags}}
	if key != "" {
		set.APIKeys = []string{key}
	}

	return set
}

func TestValidateFlagSetDocuments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		docs    []*controlplane.FlagSetDocument
		want    []problem
		noMani  bool
	}{
		{name: "no documents", docs: nil},
		{
			name: "distinct names and keys across documents",
			docs: []*controlplane.FlagSetDocument{docOf(flagSetOf("one", "k1")), docOf(flagSetOf("two", "k2"))},
		},
		{
			name: "same name in two documents: the later one has the problem",
			docs: []*controlplane.FlagSetDocument{
				docOf(flagSetOf("one", "k1")),
				docOf(flagSetOf("two", "k2"), flagSetOf("one", "k3")),
			},
			want: []problem{set(controlplane.CodeDuplicateName, "one", 1, "documents[1].flagSets[1].name")},
		},
		{
			name: "same key in two documents is reported by position",
			docs: []*controlplane.FlagSetDocument{
				docOf(flagSetOf("one", canaryKeyAcrossDocs)), docOf(flagSetOf("two", canaryKeyAcrossDocs)),
			},
			want: []problem{set(controlplane.CodeDuplicateKey, "two", 0, "documents[1].flagSets[0].apiKeys[0]")},
		},
		{
			name: "unresolved source in the second document",
			docs: []*controlplane.FlagSetDocument{
				docOf(flagSetOf("one", "")), docOf(controlplane.FlagSet{Name: "two", Sources: []string{sourceNope}}),
			},
			want: []problem{set(controlplane.CodeUnresolvedSource, "two", 0, "documents[1].flagSets[0].sources[0]")},
		},
		{
			name: "without a manifest sources are not resolved",
			docs: []*controlplane.FlagSetDocument{
				docOf(controlplane.FlagSet{Name: "two", Sources: []string{sourceNope}}),
			},
			noMani: true,
		},
		{
			name:    "a nil document",
			docs:    []*controlplane.FlagSetDocument{nil},
			wantErr: controlplane.ErrNilDocument,
		},
		{
			name:    "a document of another kind is rejected whole",
			docs:    []*controlplane.FlagSetDocument{{Kind: "Manifest"}},
			wantErr: controlplane.ErrWrongKind,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			manifest := validManifest()
			if tt.noMani {
				manifest = nil
			}

			err := controlplane.ValidateFlagSetDocuments(tt.docs, manifest)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			assert.Equal(t, tt.want, problemsOf(t, err))

			if err != nil {
				assert.NotContains(t, err.Error(), canaryKeyAcrossDocs, "a key is never printed")
			}
		})
	}
}
