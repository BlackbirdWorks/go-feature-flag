package docstest_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// markerFragment marks a block that shows part of a document: the test skips it.
const markerFragment = "fragment"

const (
	kindManifest = "Manifest"
	kindFlagSet  = "FlagSet"
	kindSegments = "Segments"
)

var documentKind = regexp.MustCompile(
	`(?m)^kind:\s*(Manifest|FlagSet|Segments)\s*$|"kind"\s*:\s*"(Manifest|FlagSet|Segments)"`,
)

// document is a manifest, flag-set or segments document found on a page.
type document struct {
	name string
	kind string
	body string
}

// kindOf returns the control-plane kind that a block declares, or "".
func kindOf(body string) string {
	match := documentKind.FindStringSubmatch(body)
	if match == nil {
		return ""
	}

	return match[1] + match[2]
}

// documents returns every control-plane document that the pages show whole: a yaml or json block, or a here-document
// of a shell block, with a `kind` of the control plane. A block marked `<!-- docs-test: fragment -->` is skipped.
func documents(t *testing.T) []document {
	t.Helper()

	var out []document

	for _, page := range pages(t) {
		for _, block := range fences(t, page) {
			if block.marker == markerFragment {
				continue
			}

			var bodies []string

			switch block.lang {
			case langYAML, "yml", "json":
				bodies = []string{block.body}
			case langShell, "bash", "sh":
				bodies = append(heredocs(block.body, "YAML"), heredocs(block.body, "JSON")...)
			}

			for _, body := range bodies {
				if kind := kindOf(body); kind != "" {
					out = append(out, document{name: block.name(), kind: kind, body: body})
				}
			}
		}
	}

	return out
}

// Every manifest, flag-set and segments document of the pages decodes strictly (no unknown field) and passes the
// validation of the control-plane package.
func TestDocumentedControlPlaneDocumentsAreValid(t *testing.T) {
	t.Parallel()

	found := map[string]int{}

	for _, doc := range documents(t) {
		found[doc.kind]++

		t.Run(doc.name, func(t *testing.T) {
			t.Parallel()

			data := []byte(doc.body)

			var info controlplane.DecodeInfo

			switch doc.kind {
			case kindManifest:
				parsed, decoded, err := controlplane.UnmarshalManifest(data)
				require.NoError(t, err)
				require.NoError(t, controlplane.ValidateManifest(parsed))

				info = decoded
			case kindFlagSet:
				parsed, decoded, err := controlplane.UnmarshalFlagSet(data)
				require.NoError(t, err)
				require.NoError(t, controlplane.ValidateFlagSet(parsed, nil))

				info = decoded
			default:
				parsed, decoded, err := controlplane.UnmarshalSegments(data)
				require.NoError(t, err)
				require.NoError(t, controlplane.ValidateSegments(parsed))

				info = decoded
			}

			assert.Empty(t, info.UnknownFields)
		})
	}

	for _, kind := range []string{kindManifest, kindFlagSet, kindSegments} {
		assert.Positive(t, found[kind], "the pages show a %s document", kind)
	}
}

// The detection finds a document by its top-level kind, in yaml and in json, and nothing else.
func TestDocumentKindDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "yaml", body: "apiVersion: semaphore/v1\nkind: FlagSet\n", want: kindFlagSet},
		{name: "json", body: `{"kind": "Manifest"}`, want: kindManifest},
		{name: "other kind", body: "kind: Deployment\n", want: ""},
		{name: "indented", body: "  kind: Segments\n", want: ""},
		{name: "marker word", body: strings.Repeat("x", 3), want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, kindOf(tt.body))
		})
	}
}
