package corehttp_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// A flag set only sees the segments of the segments objects that it lists in segmentSources: a flag of another tenant
// that names a segment of an object it does not list does not resolve it, whatever the name, and the members of the
// other tenant's segment never reach it: the name stays unresolved and the lookup is a known "not a member".
func TestE2E_FlagSetSeesOnlyItsOwnSegments(t *testing.T) {
	t.Parallel()

	const (
		otherKey  = "other-tenant-key"
		otherSegs = "segments-other"
		flagsYAML = `
uses-secret:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("secret-audience")', variation: on}
  defaultRule: {name: default, variation: off}
`
	)

	rig := newV2With(t, &v2Options{cold: true})
	rig.flags[dynFlagsObject] = []byte(flagsYAML)
	rig.flags["flags-other"] = []byte(flagsYAML)
	rig.segments = map[string][]byte{
		segObjectName: []byte(segHeaderYAML + "  - name: secret-audience\n    members: [alice]\n"),
		otherSegs:     []byte(segHeaderYAML + "  - name: unrelated\n    members: [alice]\n"),
	}
	rig.sets = []controlplane.FlagSet{
		{
			Name:           setName,
			Sources:        []string{dynFlagsObject},
			SegmentSources: []string{segObjectName},
			APIKeys:        []string{tenantKey},
		},
		{
			Name:           "other",
			Sources:        []string{"flags-other"},
			SegmentSources: []string{otherSegs},
			APIKeys:        []string{otherKey},
		},
	}

	rig.publish(t)
	rig.poll(t)

	tests := []struct {
		name      string
		key       string
		wantValue bool
	}{
		{name: "the owner of the object", key: tenantKey, wantValue: true},
		{name: "another flag set that names the same segment", key: otherKey, wantValue: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, body := ofrepValueFor(t, rig, "uses-secret", "alice", tt.key)
			require.Equal(t, http.StatusOK, code, string(body))

			assert.Contains(t, string(body), `"value":`+strconv.FormatBool(tt.wantValue))
		})
	}
}

func ofrepValueFor(t *testing.T, rig *v2, flag, id, apiKey string) (int, []byte) {
	t.Helper()

	code, _, body := do(t, http.MethodPost, rig.url+pathOFREPOne+flag,
		`{"context":{"targetingKey":"`+id+`"}}`, keyHeader(apiKey))

	return code, body
}
