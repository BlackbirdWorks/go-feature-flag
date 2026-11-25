package relay_test

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

const (
	ruleBoolTrack = "rule-bool-track"
)

func TestParseEndpoint(t *testing.T) {
	t.Parallel()

	for _, e := range relay.Endpoints() {
		got, err := relay.ParseEndpoint(string(e))
		require.NoError(t, err)
		assert.Equal(t, e, got)
	}

	for _, bad := range []string{"", "goff", "GOFF-EVAL", "ofrep"} {
		_, err := relay.ParseEndpoint(bad)
		assert.Error(t, err, bad)
	}
}

func TestEndpoint_PathAndEmitsEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ep         relay.Endpoint
		wantPath   string
		wantEvents bool
	}{
		{relay.SemaphoreEval, "/v1/feature/k/eval", true},
		{relay.SemaphoreAllFlags, "/v1/allflags", false},
		{relay.OFREPEval, "/ofrep/v1/evaluate/flags/k", true},
		{relay.OFREPBulk, "/ofrep/v1/evaluate/flags", false},
		{relay.Endpoint("bogus"), "", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.ep), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantPath, tt.ep.Path("k"))
			assert.Equal(t, tt.wantEvents, tt.ep.EmitsEvents())

			req, err := tt.ep.NewRequest("http://x", "k", 1)
			if tt.wantPath == "" {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "http://x"+tt.wantPath, req.URL.String())
			assert.NotEmpty(t, tt.ep.Body(1))
		})
	}
}

func TestNewTarget_RejectsInvalidCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ep      relay.Endpoint
		mode    loadtest.ExporterMode
		wantErr string
	}{
		{
			name:    "unknown endpoint",
			ep:      relay.Endpoint("nope"),
			mode:    loadtest.ExporterDiscard,
			wantErr: `unknown endpoint "nope" (want one of [semaphore-eval semaphore-allflags ofrep-eval ofrep-bulk])`,
		},
		{
			name:    "unknown exporter",
			ep:      relay.SemaphoreEval,
			mode:    loadtest.ExporterMode("flaky"),
			wantErr: `parsing the exporter mode of the target: unknown exporter "flaky" (want discard | slow | failing)`,
		},
		{
			name: "slow exporter on semaphore bulk",
			ep:   relay.SemaphoreAllFlags,
			mode: loadtest.ExporterSlow,
			wantErr: `endpoint "semaphore-allflags" does not emit events, so exporter "slow" would never be exercised: use` +
				` semaphore-eval or ofrep-eval`,
		},
		{
			name: "failing exporter on semaphore bulk",
			ep:   relay.SemaphoreAllFlags,
			mode: loadtest.ExporterFailing,
			wantErr: `endpoint "semaphore-allflags" does not emit events, so exporter "failing" would never be exercised: ` +
				`use semaphore-eval or ofrep-eval`,
		},
		{
			name: "slow exporter on ofrep bulk",
			ep:   relay.OFREPBulk,
			mode: loadtest.ExporterSlow,
			wantErr: `endpoint "ofrep-bulk" does not emit events, so exporter "slow" would never be exercised: use semapho` +
				`re-eval or ofrep-eval`,
		},
		{
			name: "failing exporter on ofrep bulk",
			ep:   relay.OFREPBulk,
			mode: loadtest.ExporterFailing,
			wantErr: `endpoint "ofrep-bulk" does not emit events, so exporter "failing" would never be exercised: use sema` +
				`phore-eval or ofrep-eval`,
		},
		{
			name: "slow exporter on an untracked flag",
			ep:   relay.SemaphoreEval,
			mode: loadtest.ExporterSlow,
			wantErr: `flag "rule-bool-notrack" has trackEvents disabled, so exporter "slow" would never be exercised: choo` +
				`se a flag with tracking on`,
		},
		{
			name: "failing exporter on an untracked flag",
			ep:   relay.OFREPEval,
			mode: loadtest.ExporterFailing,
			wantErr: `flag "rule-bool-notrack" has trackEvents disabled, so exporter "failing" would never be exercised: c` +
				`hoose a flag with tracking on`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flagKey := ruleBoolTrack
			if strings.Contains(tt.name, "untracked") {
				flagKey = "rule-bool-notrack"
			}

			tg, err := relay.NewTarget(relay.Options{FlagsYAML: loadtest.FlagsYAML(), ExporterMode: tt.mode},
				tt.ep, flagKey, 1)
			require.EqualError(t, err, tt.wantErr)
			assert.Nil(t, tg)
		})
	}
}

func TestTarget_Eval(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ep      relay.Endpoint
		mode    loadtest.ExporterMode
		flag    string
		wantErr bool
	}{
		{"semaphore known flag", relay.SemaphoreEval, loadtest.ExporterDiscard, ruleBoolTrack, false},
		{"semaphore unknown flag is an error", relay.SemaphoreEval, loadtest.ExporterDiscard, "does-not-exist", true},
		{"ofrep known flag", relay.OFREPEval, loadtest.ExporterDiscard, ruleBoolTrack, false},
		{"ofrep unknown flag is an error", relay.OFREPEval, loadtest.ExporterDiscard, "does-not-exist", true},
		{
			"semaphore bulk with discard exporter",
			relay.SemaphoreAllFlags,
			loadtest.ExporterDiscard,
			ruleBoolTrack,
			false,
		},
		{"ofrep bulk without exporter", relay.OFREPBulk, "", ruleBoolTrack, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tgt, err := relay.NewTarget(relay.Options{FlagsYAML: loadtest.FlagsYAML(), ExporterMode: tt.mode},
				tt.ep, tt.flag, 1)
			require.NoError(t, err)

			defer tgt.Close()

			err = tgt.Eval(0, 1)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Contains(t, tgt.Extra(), "sink_requests")
		})
	}
}

func TestRelay_CloseRemovesTempDir(t *testing.T) {
	t.Parallel()

	r, err := relay.Start(relay.Options{FlagsYAML: loadtest.FlagsYAML()})
	require.NoError(t, err)

	dir := r.Dir()
	_, err = os.Stat(dir)
	require.NoError(t, err, "the flag file must exist while the relay runs")
	r.Close()

	_, err = os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "the temp dir must be removed by Close, got %v", err)
}

func TestStart_CleansUpOnFailure(t *testing.T) {
	t.Parallel()
	// invalid flag file content makes the flagset manager fail to start
	parent := t.TempDir()
	r, err := relay.Start(relay.Options{FlagsYAML: "{{{ not yaml", TempDir: parent})
	require.Error(t, err)
	assert.Nil(t, r)

	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	assert.Empty(t, entries, "the temp dir created by Start must be removed on failure")
}

func TestTarget_ProtocolAndFlagsPerCall(t *testing.T) {
	t.Parallel()

	for _, h2c := range []bool{false, true} {
		for _, endpoint := range relay.Endpoints() {
			name := "http1 " + string(endpoint)

			wantProto := "HTTP/1.1"
			if h2c {
				name, wantProto = "h2c "+string(endpoint), "h2c"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				mode := loadtest.ExporterMode("")
				if endpoint.EmitsEvents() {
					mode = loadtest.ExporterDiscard
				}

				tgt, err := relay.NewTarget(
					relay.Options{FlagsYAML: loadtest.FlagsYAML(), ExporterMode: mode, H2C: h2c},
					endpoint,
					ruleBoolTrack,
					2,
				)
				require.NoError(t, err)

				defer tgt.Close()

				require.NoError(t, tgt.Eval(0, 1), "the response is served with the expected protocol")
				assert.Equal(t, wantProto, tgt.Protocol())

				if endpoint.EmitsEvents() {
					assert.Equal(t, 1, tgt.FlagsPerCall())
				} else {
					assert.Greater(t, tgt.FlagsPerCall(), 1, "a bulk response evaluates every flag")
				}
			})
		}
	}
}
