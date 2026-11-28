package corehttp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func featureFlagsYAML(val string) []byte {
	return []byte(fmt.Sprintf("feature:\n  variations: {v: %q}\n  defaultRule: {variation: v}\n", val))
}

func publishWithVersions(
	tb testing.TB,
	rig *v2,
	versions map[string]int,
) ([]byte, map[string]controlplane.Checksum) {
	tb.Helper()

	rig.gen++

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: rig.sets})
	require.NoError(tb, err)

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: rig.gen,
	}

	sums := map[string]controlplane.Checksum{}

	add := func(name, kind string, data []byte) {
		sum := controlplane.ChecksumOf(data)
		key, keyErr := controlplane.ObjectKey(sum, controlplane.FormatYAML)
		require.NoError(tb, keyErr)
		rig.put(key, data)

		sums[name] = sum
		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: kind, Connection: connName, Key: key, Format: controlplane.FormatYAML,
			Version: versions[name], Checksum: sum, Size: int64(len(data)),
		})
	}

	add(setsObject, controlplane.KindFlagSets, doc)

	for name, data := range rig.flags {
		add(name, controlplane.KindFlags, data)
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(tb, err)
	rig.put(manifestKey, data)

	return data, sums
}

func TestE2E_UnsupportedObjectVersionDegradesOnlyThatObject(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, &v2Options{cold: true})

	const (
		setTwo   = "two"
		keyOne   = "key-one"
		keyTwo   = "key-two"
		stateOK  = "ok"
		v2Reason = "unsupported-version"
	)

	rig.sets = []controlplane.FlagSet{
		{Name: "one", Sources: []string{flagsObject}, APIKeys: []string{keyOne}},
		{Name: setTwo, Sources: []string{flagsB}, APIKeys: []string{keyTwo}},
	}
	rig.flags = map[string][]byte{
		flagsObject: featureFlagsYAML("gen1-a"),
		flagsB:      featureFlagsYAML("gen1-b"),
	}

	eval := func(apiKey string) string {
		status, _, body := do(t, http.MethodPost, rig.url+"/ofrep/v1/evaluate/flags/feature",
			`{"context":{"targetingKey":"u1"}}`, keyHeader(apiKey))
		require.Equal(t, http.StatusOK, status, "body: %s", string(body))

		var resp model.OFREPEvaluateSuccessResponse
		require.NoError(t, json.Unmarshal(body, &resp))

		val, ok := resp.Value.(string)
		require.True(t, ok)

		return val
	}

	readInfo := func() model.ConfigInfo {
		status, _, body := do(t, http.MethodGet, rig.url+"/info", "", keyHeader(adminKey))
		require.Equal(t, http.StatusOK, status)

		var resp model.InfoResponse
		require.NoError(t, json.Unmarshal(body, &resp))
		require.NotNil(t, resp.Config)

		return *resp.Config
	}

	// 1. Generation 1: both flag sets serve their initial values.
	_, sumsGen1 := publishWithVersions(t, rig, nil)
	rig.poll(t)

	assert.Equal(t, "gen1-a", eval(keyOne))
	assert.Equal(t, "gen1-b", eval(keyTwo))

	// 2. Generation 2: flags-a bumps to version 2 (unsupported), while flags-b updates at version 1.
	rig.flags[flagsObject] = featureFlagsYAML("gen2-a")
	rig.flags[flagsB] = featureFlagsYAML("gen2-b")
	_, sumsGen2 := publishWithVersions(t, rig, map[string]int{flagsObject: 2})
	rig.poll(t)

	assert.Equal(t, "gen1-a", eval(keyOne), "dependent flag set keeps last-known-good content")
	assert.Equal(t, "gen2-b", eval(keyTwo), "unrelated flag set updates cleanly")

	info := readInfo()
	assert.Equal(t, uint64(2), info.Generation)

	byObject := make(map[string]model.ObjectInfo, len(info.Objects))
	for _, obj := range info.Objects {
		byObject[obj.Name] = obj
	}

	assert.Equal(t, model.ObjectInfo{
		Name:           flagsObject,
		Kind:           controlplane.KindFlags,
		State:          string(flagruntime.ObjectStale),
		Reason:         v2Reason,
		ChecksumHeld:   sumsGen1[flagsObject].Value,
		ChecksumListed: sumsGen2[flagsObject].Value,
		Flags:          1,
	}, byObject[flagsObject])
	assert.Equal(t, model.ObjectInfo{
		Name:           flagsB,
		Kind:           controlplane.KindFlags,
		State:          stateOK,
		ChecksumHeld:   sumsGen2[flagsB].Value,
		ChecksumListed: sumsGen2[flagsB].Value,
		Flags:          1,
	}, byObject[flagsB])

	bySet := make(map[string]model.FlagSetInfo, len(info.FlagSets))
	for _, set := range info.FlagSets {
		bySet[set.Name] = set
	}

	assert.Equal(t, "degraded", bySet["one"].State)
	assert.Equal(t, []model.ProblemInfo{{Code: "source-degraded", Subject: flagsObject}}, bySet["one"].Problems)
	assert.Equal(t, "ready", bySet[setTwo].State)
	assert.Empty(t, bySet[setTwo].Problems)

	wantMetrics := `
# HELP semaphore_object_status Source objects by state: ok, stale, missing, pending, invalid.
# TYPE semaphore_object_status gauge
semaphore_object_status{state="invalid"} 0
semaphore_object_status{state="missing"} 0
semaphore_object_status{state="ok"} 2
semaphore_object_status{state="pending"} 0
semaphore_object_status{state="stale"} 1
# HELP semaphore_resource_count Resources by kind (flagset, object, dynamicSegment, unresolvedSegment) and state.
# TYPE semaphore_resource_count gauge
semaphore_resource_count{kind="dynamicSegment",state="rejected"} 0
semaphore_resource_count{kind="dynamicSegment",state="valid"} 0
semaphore_resource_count{kind="flagset",state="degraded"} 1
semaphore_resource_count{kind="flagset",state="pending"} 0
semaphore_resource_count{kind="flagset",state="ready"} 1
semaphore_resource_count{kind="object",state="invalid"} 0
semaphore_resource_count{kind="object",state="missing"} 0
semaphore_resource_count{kind="object",state="ok"} 2
semaphore_resource_count{kind="object",state="pending"} 0
semaphore_resource_count{kind="object",state="stale"} 1
semaphore_resource_count{kind="unresolvedSegment",state="unresolved"} 0
# HELP semaphore_resource_status 1 for a resource that is not ready, by kind and name; at most 100 per kind.
# TYPE semaphore_resource_status gauge
semaphore_resource_status{kind="flagset",name="one",state="degraded"} 1
semaphore_resource_status{kind="object",name="flags-a",state="stale"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		rig.metrics.Registry,
		strings.NewReader(wantMetrics),
		"semaphore_object_status",
		"semaphore_resource_count",
		"semaphore_resource_status",
	))

	// 3. Generation 3: flags-a returns to version 1 and recovers cleanly.
	rig.flags[flagsObject] = featureFlagsYAML("gen3-a")
	publishWithVersions(t, rig, nil)
	rig.poll(t)

	assert.Equal(t, "gen3-a", eval(keyOne))

	info = readInfo()
	assert.Equal(t, uint64(3), info.Generation)

	for _, obj := range info.Objects {
		assert.Equal(t, stateOK, obj.State, obj.Name)
		assert.Empty(t, obj.Reason, obj.Name)
	}

	// 4. Generation 4: a newer manifest apiVersion still rejects the whole manifest.
	raw, _ := publishWithVersions(t, rig, nil)
	rig.put(manifestKey, bytes.Replace(raw, []byte("semaphore/v1"), []byte("semaphore/v2"), 1))
	rig.tick(t)

	assert.Eventually(t, func() bool {
		manifest := rig.core.Status().Source.Reader.Manifest

		return manifest.Outcome == manifestreader.OutcomeInvalid && manifest.Reason == v2Reason
	}, waitBound, time.Millisecond)

	assert.Equal(t, uint64(3), readInfo().Generation)
	assert.Equal(t, "gen3-a", eval(keyOne))
}
