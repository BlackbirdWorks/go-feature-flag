package s3publish_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

// noFragment fails when text holds the secret or a fragment of it of six characters or more.
func noFragment(t *testing.T, text, secret string) {
	t.Helper()

	runes := []rune(secret)
	for i := 0; i+6 <= len(runes); i++ {
		fragment := string(runes[i : i+6])
		if strings.TrimSpace(fragment) == "" {
			continue
		}

		assert.NotContains(t, text, fragment, "the text repeats a fragment of %q", secret)
	}
}

func engineCheck(obj *s3publish.Object) error {
	if obj.Kind != controlplane.KindFlags {
		return nil
	}

	return flagcheck.Check(obj.Body, obj.Format)
}

// Nothing a publication or a verification returns repeats a value of a document: an API key, a variation value, a
// query. The documents are broken in different ways, in YAML and in JSON, with quotes, backticks and newlines.
func TestPublishAndVerifyNeverRepeatTheContentOfADocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		kind    string
		format  string
		body    string
		secrets []string
	}{
		{
			name:   "flag file, query that does not compile",
			kind:   controlplane.KindFlags,
			format: controlplane.FormatYAML,
			body: "f:\n  variations: {on: true, off: false}\n  defaultRule: {variation: off}\n" +
				"  targeting:\n    - query: \"Qy7Wv`Mk3'Zx eq (\"\n      variation: on\n",
			secrets: []string{"Qy7Wv`Mk3'Zx"},
		},
		{
			name: "flag file, scalar where a mapping is expected", kind: controlplane.KindFlags,
			format: controlplane.FormatYAML, body: "f: \"Sc4Lr`9Wq2\"\n", secrets: []string{"Sc4Lr`9Wq2"},
		},
		{
			name: "flag file in JSON, variation values of different types", kind: controlplane.KindFlags,
			format:  controlplane.FormatJSON,
			body:    `{"f": {"variations": {"a": true, "b": "Jv4Nm8KdS"}, "defaultRule": {"variation": "a"}}}`,
			secrets: []string{"Jv4Nm8KdS"},
		},
		{
			name: "flag file in JSON, syntax error", kind: controlplane.KindFlags, format: controlplane.FormatJSON,
			body: `{"f": Jq7Pm3WzR}`, secrets: []string{"Jq7Pm3WzR"},
		},
		{
			name: "flag-set document, API key where a list is expected", kind: controlplane.KindFlagSets,
			format: controlplane.FormatYAML,
			body: "apiVersion: semaphore/v1\nkind: FlagSetDocument\nflagSets:\n- name: x\n  apiKeys: Ak9Zq2Wm7Xs\n" +
				"  sources: [a]\n",
			secrets: []string{"Ak9Zq2Wm7Xs"},
		},
		{
			name: "flag-set document, syntax error after an API key", kind: controlplane.KindFlagSets,
			format:  controlplane.FormatYAML,
			body:    "flagSets:\n- name: x\n  apiKeys: [Ak8Zq2Wm7Xt\n  sources: {a",
			secrets: []string{"Ak8Zq2Wm7Xt"},
		},
		{
			name: "flag-set document in JSON, API key of the wrong type", kind: controlplane.KindFlagSets,
			format: controlplane.FormatJSON,
			body: `{"apiVersion": "semaphore/v1", "kind": "FlagSetDocument", ` +
				`"flagSets": [{"name": "x", "apiKeys": "Ak7Zq2Wm7Xu", "sources": ["a"]}]}`,
			secrets: []string{"Ak7Zq2Wm7Xu"},
		},
		{
			name:    "flag-set document in JSON, syntax error",
			kind:    controlplane.KindFlagSets,
			format:  controlplane.FormatJSON,
			body:    `{"flagSets": [{"apiKeys": ["Ak6Zq2Wm7Xv"`,
			secrets: []string{"Ak6Zq2Wm7Xv"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Publishing it fails with an error that has no value of the document.
			fake := newFake()
			pub := newPublisher(t, fake, func(c *s3publish.Config) { c.Check = engineCheck })

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: []s3publish.Object{
				{Name: "doc", Kind: tt.kind, Format: tt.format, Body: []byte(tt.body)},
			}})
			require.Error(t, err)

			for _, secret := range tt.secrets {
				noFragment(t, err.Error(), secret)
			}

			// A bucket that holds it anyway (written by another tool) is reported without it.
			stored := newFake()
			key := objectKey(t, []byte(tt.body))
			stored.Seed(key, []byte(tt.body))

			manifest := &controlplane.Manifest{
				Channel:    testChannel,
				Generation: 1,
				Objects: []controlplane.ManifestObject{{
					Name: "doc", Kind: tt.kind, Connection: testConnection, Key: key, Format: tt.format,
					Checksum: controlplane.ChecksumOf([]byte(tt.body)), Size: int64(len(tt.body)),
				}},
			}

			body, marshalErr := controlplane.MarshalManifest(manifest)
			require.NoError(t, marshalErr)
			stored.Seed(manifestKey, body)

			rep, verifyErr := newPublisher(
				t,
				stored,
				func(c *s3publish.Config) { c.Check = engineCheck },
			).Verify(t.Context())
			require.NoError(t, verifyErr)
			require.NotEmpty(t, rep.Problems, "the stored document is reported")

			for _, problem := range rep.Problems {
				for _, secret := range tt.secrets {
					noFragment(t, problem.Detail+problem.Object+problem.Key, secret)
				}
			}
		})
	}
}
