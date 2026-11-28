package corehttp_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	dynFlagsObject = "flags-dynamic"
	// dynFlagsYAML has a flag whose first rule decides for one key before any segment is looked at: the evaluation of
	// that key never reaches the dynamic segment, and the flag is still not cacheable.
	dynFlagsYAML = `
dyn-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: vip, query: 'targetingKey eq "vip"', variation: on}
    - {name: audience, query: 'targetingKey in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
static-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: members, query: 'targetingKey in segment("beta")', variation: on}
  defaultRule: {name: default, variation: off}
`
	segDocGood    = segHeaderYAML + "  - name: beta\n    members: [alice]\n  - name: audience\n    connection: audiences\n"
	segDocMissing = segHeaderYAML + "  - name: beta\n    members: [alice]\n  - name: audience\n    connection: nowhere\n"
	segDocFixed   = segHeaderYAML + "  - name: beta\n    members: [alice, bob]\n" +
		"  - name: audience\n    connection: audiences\n    mode: lookup\n"
	segHeaderYAML = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n"
)

// evalBody is what an evaluation answers, for the fields the test reads.
type evalBody struct {
	Metadata map[string]any `json:"metadata"`
	Value    bool           `json:"value"`
}

func evalOFREP(t *testing.T, rig *v2, flag, key string) evalBody {
	t.Helper()

	code, body := ofrepValue(t, rig, flag, key)
	require.Equal(t, http.StatusOK, code, string(body))

	var got evalBody

	require.NoError(t, json.Unmarshal(body, &got))

	return got
}

func evalGOFF(t *testing.T, rig *v2, flag, key string) (bool, map[string]any) {
	t.Helper()

	code, _, body := do(t, http.MethodPost, rig.url+"/v1/feature/"+flag+"/eval",
		`{"evaluationContext":{"targetingKey":"`+key+`"},"defaultValue":false}`, keyHeader(tenantKey))
	require.Equal(t, http.StatusOK, code, string(body))

	var got struct {
		Metadata  map[string]any `json:"metadata"`
		Cacheable bool           `json:"cacheable"`
	}

	require.NoError(t, json.Unmarshal(body, &got))

	return got.Cacheable, got.Metadata
}

func infoOf(t *testing.T, rig *v2) *model.ConfigInfo {
	t.Helper()

	code, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)
	require.Equal(t, http.StatusOK, code)

	var info model.InfoResponse

	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)

	return info.Config
}

func segmentsInfo(t *testing.T, cfg *model.ConfigInfo) (*model.SegmentCountsInfo, string) {
	t.Helper()

	var (
		counts *model.SegmentCountsInfo
		state  string
	)

	for i := range cfg.FlagSets {
		if cfg.FlagSets[i].Name == setName {
			counts = cfg.FlagSets[i].Segments
		}
	}

	for i := range cfg.Objects {
		if cfg.Objects[i].Name == segObjectName {
			state = cfg.Objects[i].State
		}
	}

	require.NotNil(t, counts)

	return counts, state
}

// A definition that names a connection the bootstrap file does not have is rejected when its generation is built: the
// segments object keeps its last good content and the flags are evaluated as before (the dynamic segment has no
// provider yet: not a member, NOT_CONFIGURED), /info counts the rejection, and a document that is fixed is applied on
// the next poll without a restart. A flag that uses a dynamic segment is never cacheable, a flag with static segments
// only is as before; the flag configuration lists the segments of the flags that use some.
func TestE2E_DynamicDefinitionsAreCheckedAgainstTheConnections(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, v2Options{connections: audiencesConnection})
	rig.flags[dynFlagsObject] = []byte(dynFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(segDocGood)}
	rig.sets = []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject, dynFlagsObject}, SegmentSources: []string{segObjectName},
		APIKeys: []string{tenantKey},
	}}

	rig.publish(t)
	rig.poll(t)

	// a good document: the definition is valid and served
	counts, state := segmentsInfo(t, infoOf(t, rig))
	assert.Equal(t, string(flagruntime.ObjectOK), state)
	assert.Equal(t, model.SegmentCountsInfo{
		Static: 1, Dynamic: 1, DynamicStates: &model.DynamicStatesInfo{Valid: 1},
	}, *counts)

	dyn := evalOFREP(t, rig, "dyn-flag", "carol")
	assert.False(t, dyn.Value)
	assert.Equal(t, "NOT_CONFIGURED", dyn.Metadata[flagruntime.MetadataSegmentStatus])

	// the first rule decides: the dynamic segment is never reached, and the answer is still not cacheable
	vip := evalOFREP(t, rig, "dyn-flag", "vip")
	assert.True(t, vip.Value)
	assert.NotContains(t, vip.Metadata, flagruntime.MetadataCacheable)
	assert.NotContains(t, vip.Metadata, flagruntime.LegacyMetadataCacheable)

	cacheable, _ := evalGOFF(t, rig, "dyn-flag", "vip")
	assert.False(t, cacheable, "the GOFF evaluation of a flag with a dynamic segment")

	// a flag with a static segment only keeps the answer it had: it is looked up (not cacheable by its lookup), and
	// the flag that uses no segment stays cacheable
	assert.Contains(t, evalOFREP(t, rig, "bool-flag", "user-1").Metadata, flagruntime.LegacyMetadataCacheable)

	assertFlagConfigurationSegments(t, rig, map[string]string{
		"dyn-flag":    `"segments":["audience"],"requiresServerEvaluation":true`,
		"static-flag": `"segments":["beta"],"requiresServerEvaluation":false`,
	}, "bool-flag")

	// a rejected document: the last good content stays, /info counts it
	rig.segments[segObjectName] = []byte(segDocMissing)
	rig.publish(t)
	rig.poll(t)

	counts, state = segmentsInfo(t, infoOf(t, rig))
	assert.Equal(t, string(flagruntime.ObjectInvalid), state)
	assert.Equal(t, model.SegmentCountsInfo{
		Static: 1, Dynamic: 1, DynamicStates: &model.DynamicStatesInfo{Valid: 1, Rejected: 1},
	}, *counts)

	kept := evalOFREP(t, rig, "dyn-flag", "carol")
	assert.False(t, kept.Value)
	assert.Equal(t, "NOT_CONFIGURED", kept.Metadata[flagruntime.MetadataSegmentStatus])

	// the document is fixed: it is applied on the next poll, without a restart
	rig.segments[segObjectName] = []byte(segDocFixed)
	rig.publish(t)
	rig.poll(t)

	counts, state = segmentsInfo(t, infoOf(t, rig))
	assert.Equal(t, string(flagruntime.ObjectOK), state)
	assert.Equal(t, model.SegmentCountsInfo{
		Static: 1, Dynamic: 1, DynamicStates: &model.DynamicStatesInfo{Valid: 1},
	}, *counts)

	code, body := ofrepValue(t, rig, "static-flag", "bob")
	require.Equal(t, http.StatusOK, code, string(body))
	assert.Contains(t, string(body), `"value":true`, "the new members of the fixed document")
}

func assertFlagConfigurationSegments(t *testing.T, rig *v2, want map[string]string, without string) {
	t.Helper()

	code, _, body := do(t, http.MethodPost, rig.url+pathConfig, `{}`, keyHeader(tenantKey))
	require.Equal(t, http.StatusOK, code, string(body))

	var got struct {
		Flags map[string]json.RawMessage `json:"flags"`
	}

	require.NoError(t, json.Unmarshal(body, &got))

	for flag, fragment := range want {
		require.Contains(t, got.Flags, flag)
		assert.Contains(t, string(got.Flags[flag]), fragment, flag)
	}

	require.Contains(t, got.Flags, without)
	assert.NotContains(t, string(got.Flags[without]), "segments", "a flag that uses no segment has no field")
	assert.NotContains(t, string(got.Flags[without]), "requiresServerEvaluation")
}
