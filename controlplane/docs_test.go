package controlplane_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const docsPage = "../docs/go-module/controlplane.md"

// docFlags, docFlagSet and docManifest are the documents of the examples of the documentation page. The
// manifest describes the other two exactly (checksum, size, key), so the page shows a consistent set.
func docFlags() controlplane.Flags {
	return controlplane.Flags{"new-checkout": {
		Variations: map[string]any{"on": true, "off": false},
		Targeting: []controlplane.Rule{
			{Name: new("beta"), Query: new(queryBeta), Percentage: map[string]float64{"on": 100, "off": 0}},
		},
		DefaultRule: &controlplane.Rule{Variation: new(variationOff)},
	}}
}

func docFlagSet() *controlplane.FlagSetDocument {
	return &controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{
		{
			Name: "checkout", Sources: []string{nameCheckoutFlags},
			APIKeys: []string{apiKeyOne, "0b0f58a6-5e2c-45f0-a1b1-1d4a2f0c7e22"},
		},
		{Name: "no-keys-yet", Sources: []string{nameCheckoutFlags}},
	}}
}

func docManifest(flagsBody, setBody []byte) *controlplane.Manifest {
	flagsSum, setSum := controlplane.ChecksumOf(flagsBody), controlplane.ChecksumOf(setBody)

	return &controlplane.Manifest{
		Channel: channelStable, Generation: 7, CreatedAt: fixtureTime(2),
		Objects: []controlplane.ManifestObject{
			{
				Name: nameCheckoutFlags, Kind: controlplane.KindFlags, Connection: connFlagsEU, Key: keyOf(flagsSum),
				Format: controlplane.FormatYAML, Checksum: flagsSum, Size: int64(len(flagsBody)),
			},
			{
				Name: "checkout-sets", Kind: controlplane.KindFlagSets, Connection: connControl, Key: keyOf(setSum),
				Format: controlplane.FormatYAML, Version: 1, Checksum: setSum, Size: int64(len(setBody)),
			},
		},
	}
}

// docBlock returns the code block that follows the marker comment <!-- controlplane-doc: name -->.
func docBlock(t *testing.T, page, name string) string {
	t.Helper()

	_, after, found := strings.Cut(page, "<!-- controlplane-doc: "+name+" -->\n")
	require.True(t, found, "marker %q is missing from the page", name)

	_, body, found := strings.Cut(after, "```yaml\n")
	require.True(t, found)

	block, _, found := strings.Cut(body, "```")
	require.True(t, found)

	return block
}

// TestDocsExamplesAreWhatTheCodeProduces: the YAML examples of the documentation page are exactly the output of
// the Marshal functions and they validate.
func TestDocsExamplesAreWhatTheCodeProduces(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(docsPage)
	require.NoError(t, err)

	page := string(raw)

	flagsBody, err := controlplane.MarshalFlags(docFlags())
	require.NoError(t, err)

	setBody, err := controlplane.MarshalFlagSet(docFlagSet())
	require.NoError(t, err)

	manifestBody, err := controlplane.MarshalManifest(docManifest(flagsBody, setBody))
	require.NoError(t, err)

	assert.Equal(t, string(manifestBody), docBlock(t, page, "manifest"))
	assert.Equal(t, string(setBody), docBlock(t, page, "flagset"))
	assert.Equal(t, string(flagsBody), docBlock(t, page, "flags"))

	manifest, info, err := controlplane.UnmarshalManifest([]byte(docBlock(t, page, "manifest")))
	require.NoError(t, err)
	assert.Empty(t, info.UnknownFields)
	require.NoError(t, controlplane.ValidateManifest(manifest))

	set, _, err := controlplane.UnmarshalFlagSet([]byte(docBlock(t, page, "flagset")))
	require.NoError(t, err)
	require.NoError(t, controlplane.ValidateFlagSet(set, manifest))

	flags, _, err := controlplane.UnmarshalFlags([]byte(docBlock(t, page, "flags")), controlplane.FormatYAML)
	require.NoError(t, err)
	require.NoError(t, controlplane.ValidateFlags(flags))

	for _, object := range manifest.Objects {
		bodies := map[string]string{
			nameCheckoutFlags: docBlock(t, page, "flags"),
			"checkout-sets":   docBlock(t, page, "flagset"),
		}
		body := bodies[object.Name]
		require.NoError(t, controlplane.VerifyChecksum([]byte(body), object.Checksum), object.Name)
		assert.Equal(t, int64(len(body)), object.Size)
	}
}
