package relayapp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

const (
	publicFlag    = "pub"
	privateFlag   = flagKey
	evalBody      = `{"evaluationContext":{"key":"u"},"defaultValue":0}`
	contextBody   = `{"evaluationContext":{"key":"u"}}`
	collectorPath = "/v1/data/collector"
)

// keysOf returns the sorted keys of the object at the path of a JSON body.
func keysOf(t *testing.T, body, path string) []string {
	t.Helper()

	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &doc))

	var inner map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc[path], &inner))

	keys := make([]string, 0, len(inner))
	for key := range inner {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func collectorBody(keys ...string) string {
	events := make([]string, 0, len(keys))
	for _, key := range keys {
		events = append(events, `{"kind":"feature","contextKind":"user","userKey":"u","creationDate":1,"key":"`+key+
			`","variation":"v","value":1,"default":0}`)
	}

	return `{"meta":{"app":"x"},"events":[` + strings.Join(events, ",") + `]}`
}

func notPublicLine(count int) string {
	return fmt.Sprintf(`semaphore_events_dropped_total{reason="not_public"} %d`, count)
}

func droppedNotPublic(t *testing.T, rig *relayRig) string {
	t.Helper()

	_, metrics := get(t, rig.url+"/metrics", nil)

	for line := range strings.SplitSeq(metrics, "\n") {
		if strings.HasPrefix(line, `semaphore_events_dropped_total{reason="not_public"}`) {
			return line
		}
	}

	return ""
}

// A caller without a key is a caller of the public flags only: on every endpoint that accepts it, it can not read or
// write anything about a flag that is not public (the flag f of the flag set with a key), and the answer for a private
// flag is the one of a flag that does not exist.
func TestRunKeylessCallerSeesOnlyThePublicFlags(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\n"})
	rig.awaitGeneration(1)

	rig.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7),
	})
	rig.poll()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		// wantContains and wantAbsent are texts the body has and does not have; wantKeys are the keys of the flags
		// object of the body, when it has one
		wantContains []string
		wantAbsent   []string
		wantKeys     []string
		wantStatus   int
	}{
		{
			name: "the single evaluation of a private flag", method: http.MethodPost, path: "/v1/feature/f/eval",
			body: evalBody, wantStatus: http.StatusOK, wantContains: []string{`"errorCode":"FLAG_NOT_FOUND"`},
		},
		{
			name: "the single evaluation of a public flag", method: http.MethodPost, path: "/v1/feature/pub/eval",
			body: evalBody, wantStatus: http.StatusOK, wantContains: []string{`"value":7`},
		},
		{
			name: "all the flags", method: http.MethodPost, path: "/v1/allflags", body: contextBody,
			wantStatus: http.StatusOK, wantKeys: []string{publicFlag},
		},
		{
			name: "the change hashes", method: http.MethodGet, path: "/v1/flag/change",
			wantStatus: http.StatusOK, wantKeys: []string{publicFlag},
		},
		{
			name: "the configuration, asked for both flags", method: http.MethodPost, path: "/v1/flag/configuration",
			body: `{"flags":["f","pub"]}`, wantStatus: http.StatusOK, wantKeys: []string{publicFlag},
		},
		{
			name: "the manifest", method: http.MethodGet, path: "/openfeature/v0/manifest",
			wantStatus: http.StatusOK, wantAbsent: []string{`"f"`},
		},
		{
			name: "the OFREP evaluation of a private flag", method: http.MethodPost, path: evalPath, body: bulkBody,
			wantStatus: http.StatusNotFound, wantContains: []string{`"errorCode":"FLAG_NOT_FOUND"`},
		},
		{
			name: "the OFREP bulk evaluation", method: http.MethodPost, path: anonymousBulkPath, body: bulkBody,
			wantStatus: http.StatusOK, wantContains: []string{`"key":"pub"`}, wantAbsent: []string{`"key":"f"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				status int
				body   string
			)

			if tt.method == http.MethodGet {
				status, body = get(t, rig.url+tt.path, http.Header{headerAccept: {mimeJSON}})
			} else {
				status, body = doRequest(t, tt.method, rig.url+tt.path, tt.body, jsonHeader())
			}

			assert.Equal(t, tt.wantStatus, status)

			for _, want := range tt.wantContains {
				assert.Contains(t, body, want)
			}

			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, body, absent)
			}

			if tt.wantKeys != nil {
				assert.Equal(t, tt.wantKeys, keysOf(t, body, "flags"))
			}
		})
	}
}

// The collector of a caller without a key: the events of a private flag are dropped and counted, and the answer keeps
// the shape it always has.
func TestRunKeylessCollectorDropsTheEventsOfPrivateFlags(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\n"})
	rig.awaitGeneration(1)

	rig.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7),
	})
	rig.poll()

	assert.Equal(
		t,
		`semaphore_events_dropped_total{reason="not_public"} 0`,
		droppedNotPublic(t, rig),
		"nothing dropped yet",
	)

	status, body := doRequest(
		t,
		http.MethodPost,
		rig.url+collectorPath,
		collectorBody(privateFlag, publicFlag, "nope"),
		jsonHeader(),
	)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"ingestedContentCount"`)
	assert.Equal(t, notPublicLine(2), droppedNotPublic(t, rig),
		"the events of the private flag and of the flag that does not exist, counted")

	// a caller with the key of the flag set is not restricted to the public flags: nothing more is dropped
	keyed := http.Header{headerAPIKey: {tenantKey}, headerContentType: {mimeJSON}}
	status, _ = doRequest(t, http.MethodPost, rig.url+collectorPath, collectorBody(privateFlag), keyed)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, notPublicLine(2), droppedNotPublic(t, rig))
}

// A generation in which two public flag sets define one flag key is rejected with its code, the refresh says so and
// says which generation the relay runs.
func TestRunRefreshReportsARejectedPublicConflict(t *testing.T) {
	t.Parallel()

	rig := startRelay(t, &rigOptions{extra: "auth:\n  adminKeys: [" + adminKey + "]\n"})
	rig.awaitGeneration(1)

	headers := http.Header{headerAPIKey: {adminKey}}

	rig.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7),
	})

	status, body := doRequest(t, http.MethodPost, rig.url+refreshPath, "", headers)
	require.Equal(t, http.StatusOK, status, body)
	assert.JSONEq(t, `{"result":"updated","generation":2,"refreshed":true,"changed":true,"applied":true,`+
		`"appliedGeneration":2}`, body)

	rig.store.publishDocs(publicSets(openSet, "other"), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7), otherObject: flagYAMLOf(publicFlag, 8),
	})

	status, body = doRequest(t, http.MethodPost, rig.url+refreshPath, "", headers)
	require.Equal(t, http.StatusOK, status, body)
	assert.JSONEq(t, `{"result":"updated","generation":3,"refreshed":true,"changed":true,"applied":false,`+
		`"appliedGeneration":2,"rejection":"`+flagruntime.CodeBuildFailed+`"}`, body)
}
