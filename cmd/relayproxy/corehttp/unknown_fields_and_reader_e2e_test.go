package corehttp_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

func publishManifest(rig *v2, raw []byte) {
	rig.put(manifestKey, raw)
}

func nextManifestBytes(t *testing.T, rig *v2) []byte {
	t.Helper()

	rig.gen++

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: rig.sets})
	require.NoError(t, err)

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: rig.gen,
	}

	add := func(name, kind string, data []byte) {
		sum := controlplane.ChecksumOf(data)
		key, keyErr := controlplane.ObjectKey(sum, controlplane.FormatYAML)
		require.NoError(t, keyErr)
		rig.put(key, data)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: kind, Connection: connName, Key: key, Format: controlplane.FormatYAML,
			Checksum: sum, Size: int64(len(data)),
		})
	}

	add(setsObject, controlplane.KindFlagSets, doc)

	for name, data := range rig.flags {
		add(name, controlplane.KindFlags, data)
	}

	for name, data := range rig.segments {
		add(name, controlplane.KindSegments, data)
	}

	raw, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)

	return raw
}

func TestE2E_UnknownFieldsInAllThreePlaces(t *testing.T) {
	t.Parallel()

	logs := &lockedBuffer{}
	rig := newV2With(t, &v2Options{cold: true, logger: newTestLogger(logs)})

	const (
		secretValue = "secret-value-never-logged"
		segsObject  = "segs"
	)

	flagWithExtra := "bool-flag:\n  futureFlag: " + secretValue + "\n" +
		"  variations: {enabled: true, disabled: false}\n" +
		"  defaultRule: {extraRule: 1, name: default, variation: enabled}\n"
	rig.flags[flagsObject] = []byte(flagWithExtra)
	rig.segments = map[string][]byte{
		segsObject: []byte("apiVersion: semaphore/v1\nkind: Segments\nextraSeg: true\n" +
			"segments:\n  - name: beta\n    members: [u1]\n"),
	}
	rig.sets = []controlplane.FlagSet{{Name: setName, Sources: []string{flagsObject}, APIKeys: []string{tenantKey}}}

	// Also inject an unknown field into the flagSets object and the manifest itself.
	setsWithExtra := []byte("apiVersion: semaphore/v1\nkind: FlagSet\nextraSetField: " + secretValue +
		"\nflagSets:\n  - name: " + setName + "\n    sources: [" + flagsObject + "]\n    apiKeys: [" + tenantKey + "]\n")
	setsSum := controlplane.ChecksumOf(setsWithExtra)
	setsKey, err := controlplane.ObjectKey(setsSum, controlplane.FormatYAML)
	require.NoError(t, err)
	rig.put(setsKey, setsWithExtra)

	flagsSum := controlplane.ChecksumOf(rig.flags[flagsObject])
	flagsKey, err := controlplane.ObjectKey(flagsSum, controlplane.FormatYAML)
	require.NoError(t, err)
	rig.put(flagsKey, rig.flags[flagsObject])

	segsSum := controlplane.ChecksumOf(rig.segments[segsObject])
	segsKey, err := controlplane.ObjectKey(segsSum, controlplane.FormatYAML)
	require.NoError(t, err)
	rig.put(segsKey, rig.segments[segsObject])

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: channelName,
		Generation: 1,
		Objects: []controlplane.ManifestObject{
			{
				Name: setsObject, Kind: controlplane.KindFlagSets, Connection: connName, Key: setsKey,
				Format: controlplane.FormatYAML, Checksum: setsSum, Size: int64(len(setsWithExtra)),
			},
			{
				Name: flagsObject, Kind: controlplane.KindFlags, Connection: connName, Key: flagsKey,
				Format: controlplane.FormatYAML, Checksum: flagsSum, Size: int64(len(rig.flags[flagsObject])),
			},
			{
				Name: segsObject, Kind: controlplane.KindSegments, Connection: connName, Key: segsKey,
				Format: controlplane.FormatYAML, Checksum: segsSum, Size: int64(len(rig.segments[segsObject])),
			},
		},
	}
	rig.gen = 1
	rawManifest, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)

	rawManifest = append(rawManifest, []byte("extraManifestA: "+secretValue+"\nextraManifestB: 2\n")...)
	publishManifest(rig, rawManifest)
	rig.poll(t)

	// Flags still evaluate cleanly.
	evalCode, _, evalBody := do(t, http.MethodPost, rig.url+pathOFREPOne+"bool-flag",
		`{"context":{"targetingKey":"u"}}`, keyHeader(tenantKey))
	require.Equal(t, http.StatusOK, evalCode)
	assert.Contains(t, string(evalBody), `"value":true`)

	// 1. /info reports counts and bounded paths per kind, without values.
	code, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, string(body), secretValue)

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)
	assert.Equal(t, []model.UnknownFieldsInfo{
		{Kind: controlplane.KindFlagSets, Fields: []string{setsObject + "/extraSetField"}, Count: 1},
		{
			Kind:   controlplane.KindFlags,
			Fields: []string{flagsObject + "/bool-flag.defaultRule.extraRule", flagsObject + "/bool-flag.futureFlag"},
			Count:  2,
		},
		{
			Kind:   strings.ToLower(controlplane.DocumentKindManifest),
			Fields: []string{"extraManifestA", "extraManifestB"},
			Count:  2,
		},
		{Kind: controlplane.KindSegments, Fields: []string{"segs/extraSeg"}, Count: 1},
	}, info.Config.UnknownFields)

	// 2. Prometheus gauge exports all four kinds.
	helpUnknown := "# HELP semaphore_config_unknown_fields Unknown fields counted in control-plane documents by " +
		"document kind (flagSets, flags, manifest, segments).\n"
	wantMetrics := helpUnknown + `
# TYPE semaphore_config_unknown_fields gauge
semaphore_config_unknown_fields{kind="flagSets"} 1
semaphore_config_unknown_fields{kind="flags"} 2
semaphore_config_unknown_fields{kind="manifest"} 2
semaphore_config_unknown_fields{kind="segments"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		rig.metrics.Registry, strings.NewReader(wantMetrics), "semaphore_config_unknown_fields",
	))

	// 3. Log emitted once per kind when counts changed, never values.
	logText := logs.all()
	assert.NotContains(t, logText, secretValue)
	assert.Equal(t, 4, strings.Count(logText, `msg="unknown fields in control-plane documents"`))

	// An unchanged poll (304) writes no extra unknown-fields log lines.
	rig.tick(t)
	assert.Equal(t, 4, strings.Count(logs.all(), `msg="unknown fields in control-plane documents"`))

	// Clean documents clear /info, metrics, and write one cleared line per kind.
	rig.flags[flagsObject] = []byte(flagsYAML)
	rig.segments = map[string][]byte{
		segsObject: []byte("apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [u1]\n"),
	}
	rig.publish(t)
	rig.poll(t)

	_, _, body = do(t, http.MethodGet, rig.url+"/info", "", nil)

	var cleared model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &cleared))
	require.NotNil(t, cleared.Config)
	assert.Empty(t, cleared.Config.UnknownFields)

	wantZero := helpUnknown + `
# TYPE semaphore_config_unknown_fields gauge
semaphore_config_unknown_fields{kind="flagSets"} 0
semaphore_config_unknown_fields{kind="flags"} 0
semaphore_config_unknown_fields{kind="manifest"} 0
semaphore_config_unknown_fields{kind="segments"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		rig.metrics.Registry, strings.NewReader(wantZero), "semaphore_config_unknown_fields",
	))
	assert.Equal(t, 4, strings.Count(logs.all(), `msg="unknown fields cleared in control-plane documents"`))
}

func TestE2E_ReaderRejectionsInInfoAndMetrics(t *testing.T) {
	t.Parallel()

	rig := newV2With(t, &v2Options{cold: true})
	rig.publish(t)
	rig.poll(t)

	// Start with gen 2 so gen 1 is a valid lower-generation manifest.
	rig.publish(t)
	rig.poll(t)

	rejections := []struct {
		name        string
		wantOutcome string
		wantReason  string
		raw         []byte
	}{
		{
			name:        "unsupported apiVersion",
			wantOutcome: manifestreader.OutcomeInvalid,
			wantReason:  string(controlplane.CodeUnsupportedVersion),
			raw: []byte(
				"apiVersion: semaphore/v2\nkind: Manifest\nchannel: stable\ngeneration: 3\nobjects: []\n",
			),
		},
		{
			name:        "wrong kind",
			wantOutcome: manifestreader.OutcomeInvalid,
			wantReason:  "wrong-kind",
			raw:         []byte("apiVersion: semaphore/v1\nkind: FlagSet\nflagSets: []\n"),
		},
		{
			name:        "channel mismatch",
			wantOutcome: manifestreader.OutcomeChannelMismatch,
			wantReason:  "channel",
			raw: []byte(
				"apiVersion: semaphore/v1\nkind: Manifest\nchannel: wrong\ngeneration: 3\nobjects: []\n",
			),
		},
		{
			name:        "lower generation",
			wantOutcome: manifestreader.OutcomeLowerGeneration,
			wantReason:  "generation",
			raw: []byte(
				"apiVersion: semaphore/v1\nkind: Manifest\nchannel: stable\ngeneration: 1\nobjects: []\n",
			),
		},
	}

	wantAt := clock.Now().UTC()

	for _, rej := range rejections {
		publishManifest(rig, rej.raw)
		rig.tick(t)

		code, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)
		require.Equal(t, http.StatusOK, code)

		var info model.InfoResponse
		require.NoError(t, json.Unmarshal(body, &info))
		require.NotNil(t, info.Config)
		assert.Equal(t, uint64(2), info.Config.Generation, rej.name)
		assert.Nil(t, info.Config.LastRejected, rej.name)
		require.NotNil(t, info.Config.Reader, rej.name)
		assert.Equal(t, &model.ReaderInfo{
			At: &wantAt, Outcome: rej.wantOutcome, Reason: rej.wantReason,
		}, info.Config.Reader, rej.name)
	}

	helpRej := "# HELP semaphore_config_reader_rejections_total Manifests rejected by the manifest reader " +
		"before reaching the engine, by closed reason.\n"
	wantRejections := helpRej + `
# TYPE semaphore_config_reader_rejections_total counter
semaphore_config_reader_rejections_total{reason="channel-mismatch"} 1
semaphore_config_reader_rejections_total{reason="decode"} 0
semaphore_config_reader_rejections_total{reason="duplicate-name"} 0
semaphore_config_reader_rejections_total{reason="generation-conflict"} 0
semaphore_config_reader_rejections_total{reason="invalid"} 0
semaphore_config_reader_rejections_total{reason="invalid-checksum"} 0
semaphore_config_reader_rejections_total{reason="invalid-name"} 0
semaphore_config_reader_rejections_total{reason="invalid-value"} 0
semaphore_config_reader_rejections_total{reason="lower-generation"} 1
semaphore_config_reader_rejections_total{reason="missing-value"} 0
semaphore_config_reader_rejections_total{reason="too-large-total"} 0
semaphore_config_reader_rejections_total{reason="too-many-objects"} 0
semaphore_config_reader_rejections_total{reason="unsupported-version"} 1
semaphore_config_reader_rejections_total{reason="wrong-kind"} 1
`
	require.NoError(t, testutil.GatherAndCompare(
		rig.metrics.Registry, strings.NewReader(wantRejections), "semaphore_config_reader_rejections_total",
	))

	// A valid higher generation clears config.reader.reason while the counter stays monotonic.
	publishManifest(rig, nextManifestBytes(t, rig))
	rig.poll(t)

	_, _, body := do(t, http.MethodGet, rig.url+"/info", "", nil)

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config.Reader)
	assert.Equal(t, &model.ReaderInfo{At: &wantAt, Outcome: manifestreader.OutcomeAccepted}, info.Config.Reader)
	require.NoError(t, testutil.GatherAndCompare(
		rig.metrics.Registry, strings.NewReader(wantRejections), "semaphore_config_reader_rejections_total",
	))
}
