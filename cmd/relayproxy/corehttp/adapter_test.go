package corehttp_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/corehttp"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

const (
	pendingKey  = "pending-key"
	pendingName = "pending"
	userCtx     = `{"context":{"targetingKey":"user-1","tier":"vip"}}`
)

func TestAdapter_Resolver(t *testing.T) {
	t.Parallel()

	rig := newV2(t)
	adapter := corehttp.NewAdapter(rig.core)

	tests := []struct {
		name         string
		key          string
		wantName     string
		wantErr      string
		wantKey      bool
		wantAdminKey bool
	}{
		{name: "a flag set key", key: tenantKey, wantName: setName, wantKey: true},
		{name: "an admin key has no flag set", key: adminKey, wantErr: "flagset not found for API key",
			wantKey: true, wantAdminKey: true},
		{name: "an unknown key", key: "unknown-key", wantErr: "flagset not found for API key"},
		{name: "an empty key", key: "", wantErr: "no API key provided"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name, nameErr := adapter.FlagSetName(tt.key)
			flagSet, setErr := adapter.FlagSet(tt.key)

			assert.Equal(t, tt.wantName, name)
			assert.Equal(t, tt.wantKey, adapter.KeyExists(tt.key))
			assert.Equal(t, tt.wantAdminKey, adapter.AdminKeyExists(tt.key))

			if tt.wantErr != "" {
				require.EqualError(t, nameErr, tt.wantErr)
				require.EqualError(t, setErr, tt.wantErr)
				assert.Nil(t, flagSet, "no typed nil in the interface")

				return
			}

			require.NoError(t, nameErr)
			require.NoError(t, setErr)
			assert.NotNil(t, flagSet)
		})
	}

	assert.False(t, adapter.IsDefaultFlagSet())
	assert.True(t, adapter.AuthenticationEnabled())
}

func TestAdapter_FlagSetView(t *testing.T) {
	t.Parallel()

	rig := newV2(t)
	adapter := corehttp.NewAdapter(rig.core)

	flagSet, err := adapter.FlagSet(tenantKey)
	require.NoError(t, err)

	again, err := adapter.FlagSet(tenantKey)
	require.NoError(t, err)
	assert.Same(t, flagSet, again, "a flag set has one view per runtime")

	set := rig.core.Holder().Load().FlagSets[setName]
	assert.Equal(t, set.ModifiedAt, flagSet.GetCacheRefreshDate())
	assert.Empty(t, flagSet.GetEvaluationContextEnrichment())
	assert.False(t, flagSet.ForceRefresh())

	flags, err := flagSet.GetFlagsFromCache()
	require.NoError(t, err)
	assert.Len(t, flags, len(set.Flags))

	flagsCtx, err := flagSet.GetFlagsFromCacheWithContext(t.Context())
	require.NoError(t, err)
	assert.Len(t, flagsCtx, len(flags))

	res, err := flagSet.RawVariation("string-flag", ffcontext.NewEvaluationContext("u"), nil)
	require.NoError(t, err)
	assert.Equal(t, "alpha", res.Value)

	_, err = flagSet.RawVariation("no-such-flag", ffcontext.NewEvaluationContext("u"), "fallback")
	require.Error(t, err)

	states := flagSet.GetFlagStates(ffcontext.NewEvaluationContext("u"), []string{"string-flag"})
	assert.Len(t, states.GetFlags(), 1)

	all := flagSet.AllFlagsState(ffcontext.NewEvaluationContext("u"))
	assert.Len(t, all.GetFlags(), len(set.Flags))

	// the exporters take feature events only: a tracking event is dropped
	flagSet.CollectTrackingEventData(&exporter.TrackingEvent{})
	assert.Empty(t, rig.events.got)
}

func TestServer_Events(t *testing.T) {
	t.Parallel()

	rig := newV2(t)

	// the evaluation of a flag that tracks its events reaches the exporters through the evaluator
	status, _, _ := do(t, http.MethodPost, rig.url+pathOFREPOne+"bool-flag", userCtx, keyHeader(tenantKey))
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "bool-flag", awaitEvent(t, rig).Key)

	// an event that a client collects goes to the exporters of the core
	event := `{"events":[{"kind":"feature","contextKind":"user","userKey":"client","creationDate":1680246000,` +
		`"key":"client-flag","variation":"v","value":true,"default":false,"version":"1","source":"PROVIDER_CACHE"}]}`
	status, _, body := do(t, http.MethodPost, rig.url+pathCollector, event, keyHeader(tenantKey))
	require.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"ingestedContentCount":1}`, string(body))
	assert.Equal(t, "client-flag", awaitEvent(t, rig).Key)
}

func awaitEvent(t *testing.T, rig *v2) *exporter.FeatureEvent {
	t.Helper()

	select {
	case event := <-rig.events.got:
		return event
	case <-time.After(waitBound):
		require.FailNow(t, "no event reached the exporters")
	}

	return nil
}

func TestServer_Monitoring(t *testing.T) {
	t.Parallel()

	rig := newV2(t)

	status, _, body := do(t, http.MethodGet, rig.url+"/health", "", nil)
	assert.Equal(t, http.StatusOK, status)
	assert.JSONEq(t, `{"initialized":true}`, string(body))

	status, _, body = do(t, http.MethodGet, rig.url+"/info", "", nil)
	require.Equal(t, http.StatusOK, status)

	var info struct {
		Flagsets map[string]string `json:"flagsets"`
	}

	require.NoError(t, json.Unmarshal(body, &info))
	assert.Contains(t, info.Flagsets, setName)
}

// TestServer_FlagSetLifecycle follows a flag set through the states the handlers answer: unavailable (known, never
// loaded), serving, removed.
func TestServer_FlagSetLifecycle(t *testing.T) {
	t.Parallel()

	rig := newV2(t)

	// a flag set whose source is not published: its key routes to it, it serves no flag
	rig.sets = append(rig.sets, controlplane.FlagSet{Name: pendingName, Sources: []string{"not-published"},
		APIKeys: []string{pendingKey}})
	rig.publish(t)
	rig.poll(t)

	status, _, body := do(t, http.MethodPost, rig.url+pathOFREPOne+"bool-flag", userCtx, keyHeader(pendingKey))
	assert.Equal(t, http.StatusNotFound, status, "an unavailable flag set: flag not found")
	assert.Contains(t, string(body), "FLAG_NOT_FOUND")

	status, _, body = do(t, http.MethodPost, rig.url+pathOFREPBulk, userCtx, keyHeader(pendingKey))
	assert.Equal(t, http.StatusOK, status, "an unavailable flag set: the bulk answers empty")
	assert.JSONEq(t, `{"flags":[]}`, string(body))

	status, _, _ = do(t, http.MethodPost, rig.url+pathOFREPOne+"bool-flag", userCtx, keyHeader(tenantKey))
	assert.Equal(t, http.StatusOK, status)

	// a removed flag set: its key is unknown, the others keep serving
	rig.sets = rig.sets[1:]
	rig.publish(t)
	rig.poll(t)

	status, _, body = do(t, http.MethodPost, rig.url+pathOFREPOne+"bool-flag", userCtx, keyHeader(tenantKey))
	assert.Equal(t, http.StatusUnauthorized, status, "a removed flag set answers 401")
	assert.JSONEq(t, `{"message":"Unauthorized"}`, string(body))

	status, _, _ = do(t, http.MethodPost, rig.url+pathOFREPBulk, userCtx, keyHeader(tenantKey))
	assert.Equal(t, http.StatusUnauthorized, status)

	status, _, _ = do(t, http.MethodPost, rig.url+pathOFREPOne+"bool-flag", userCtx, keyHeader(pendingKey))
	assert.Equal(t, http.StatusNotFound, status)
}

// TestServer_HandlersDoNotChangeResults: the results of the evaluations are shared, the handlers only read them.
func TestServer_HandlersDoNotChangeResults(t *testing.T) {
	t.Parallel()

	rig := newV2(t)
	set := rig.core.Holder().Load().FlagSets[setName]

	tableBefore, err := json.Marshal(set.Flags)
	require.NoError(t, err)

	cases := allCases()

	first := make([][]byte, len(cases))

	for idx, tt := range cases {
		_, _, first[idx] = do(t, tt.method, rig.url+tt.path, tt.body, tt.headers)
	}

	for idx, tt := range cases {
		_, _, again := do(t, tt.method, rig.url+tt.path, tt.body, tt.headers)
		assert.Equal(t, string(first[idx]), string(again), tt.name)
	}

	tableAfter, err := json.Marshal(set.Flags)
	require.NoError(t, err)
	assert.JSONEq(t, string(tableBefore), string(tableAfter), "the shared table is unchanged")
	assert.Same(t, set, rig.core.Holder().Load().FlagSets[setName], "no new runtime was needed")

	// the value of an evaluation, evaluated again after the handlers added their keys to its metadata
	flagSet, err := corehttp.NewAdapter(rig.core).FlagSet(tenantKey)
	require.NoError(t, err)

	for key := range set.Flags {
		one, oneErr := flagSet.RawVariation(key, ffcontext.NewEvaluationContext("u"), nil)
		two, twoErr := flagSet.RawVariation(key, ffcontext.NewEvaluationContext("u"), nil)
		assert.Equal(t, oneErr, twoErr, key)
		assert.Equal(t, one, two, key)
	}
}

// TestE2E_ChangeAfterOnePoll serves the evaluation API over HTTP from a core on a file store: an object changes and
// the new value is served after one poll, without a restart.
func TestE2E_ChangeAfterOnePoll(t *testing.T) {
	t.Parallel()

	rig := newV2(t)

	value := func() string {
		status, _, body := do(t, http.MethodPost, rig.url+pathOFREPOne+"string-flag", userCtx, bearer(tenantKey))
		require.Equal(t, http.StatusOK, status)

		var res struct {
			Value string `json:"value"`
		}

		require.NoError(t, json.Unmarshal(body, &res))

		return res.Value
	}

	assert.Equal(t, "alpha", value())

	// a client of the change stream is told about the change
	ctx, cancel := context.WithTimeout(t.Context(), waitBound)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		rig.url+"/stream/v1/sse/flag/change?apiKey="+tenantKey, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer func() { require.NoError(t, resp.Body.Close()) }()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	lines := bufio.NewScanner(resp.Body)
	// the server flushes its first comment once the client is registered
	require.True(t, lines.Scan(), "the stream did not open")

	rig.flags[flagsObject] = []byte(strings.Replace(flagsYAML, "a: alpha", "a: gamma", 1))
	rig.publish(t)
	rig.poll(t)

	assert.Equal(t, "gamma", value())

	var data string

	for lines.Scan() {
		if after, ok := strings.CutPrefix(lines.Text(), "data:"); ok {
			data = after

			break
		}
	}

	assert.Contains(t, data, "refetchEvaluation")
	cancel()
}
