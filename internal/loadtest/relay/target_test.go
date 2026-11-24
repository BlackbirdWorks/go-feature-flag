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

func TestParseEndpoint(t *testing.T) {
	t.Parallel()
	for _, e := range relay.Endpoints {
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
		ep     relay.Endpoint
		path   string
		events bool
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
			assert.Equal(t, tt.path, tt.ep.Path("k"))
			assert.Equal(t, tt.events, tt.ep.EmitsEvents())
			req, err := tt.ep.NewRequest("http://x", "k", 1)
			if tt.path == "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "http://x"+tt.path, req.URL.String())
			assert.NotEmpty(t, tt.ep.Body(1))
		})
	}
}

func TestNewTarget_RejectsInvalidCombinations(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ep   relay.Endpoint
		mode loadtest.ExporterMode
	}{
		{"unknown endpoint", relay.Endpoint("nope"), loadtest.ExporterDiscard},
		{"unknown exporter", relay.SemaphoreEval, loadtest.ExporterMode("flaky")},
		{"slow exporter on semaphore bulk", relay.SemaphoreAllFlags, loadtest.ExporterSlow},
		{"failing exporter on semaphore bulk", relay.SemaphoreAllFlags, loadtest.ExporterFailing},
		{"slow exporter on ofrep bulk", relay.OFREPBulk, loadtest.ExporterSlow},
		{"failing exporter on ofrep bulk", relay.OFREPBulk, loadtest.ExporterFailing},
		{"slow exporter on an untracked flag", relay.SemaphoreEval, loadtest.ExporterSlow},
		{"failing exporter on an untracked flag", relay.OFREPEval, loadtest.ExporterFailing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			flagKey := "rule-bool-track"
			if strings.Contains(tt.name, "untracked") {
				flagKey = "rule-bool-notrack"
			}
			tg, err := relay.NewTarget(relay.Options{FlagsYAML: loadtest.FlagsYAML(), ExporterMode: tt.mode},
				tt.ep, flagKey, 1)
			require.Error(t, err)
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
		{"semaphore known flag", relay.SemaphoreEval, loadtest.ExporterDiscard, "rule-bool-track", false},
		{"semaphore unknown flag is an error", relay.SemaphoreEval, loadtest.ExporterDiscard, "does-not-exist", true},
		{"ofrep known flag", relay.OFREPEval, loadtest.ExporterDiscard, "rule-bool-track", false},
		{"ofrep unknown flag is an error", relay.OFREPEval, loadtest.ExporterDiscard, "does-not-exist", true},
		{"semaphore bulk with discard exporter", relay.SemaphoreAllFlags, loadtest.ExporterDiscard, "rule-bool-track", false},
		{"ofrep bulk without exporter", relay.OFREPBulk, "", "rule-bool-track", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tg, err := relay.NewTarget(relay.Options{FlagsYAML: loadtest.FlagsYAML(), ExporterMode: tt.mode},
				tt.ep, tt.flag, 1)
			require.NoError(t, err)
			defer tg.Close()
			err = tg.Eval(0, 1)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Contains(t, tg.Extra(), "sink_requests")
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
		for _, ep := range relay.Endpoints {
			name := "http1 " + string(ep)
			wantProto := "HTTP/1.1"
			if h2c {
				name, wantProto = "h2c "+string(ep), "h2c"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				mode := loadtest.ExporterMode("")
				if ep.EmitsEvents() {
					mode = loadtest.ExporterDiscard
				}
				tg, err := relay.NewTarget(relay.Options{FlagsYAML: loadtest.FlagsYAML(), ExporterMode: mode, H2C: h2c},
					ep, "rule-bool-track", 2)
				require.NoError(t, err)
				defer tg.Close()
				require.NoError(t, tg.Eval(0, 1), "the response is served with the expected protocol")
				assert.Equal(t, wantProto, tg.Protocol())
				if ep.EmitsEvents() {
					assert.Equal(t, 1, tg.FlagsPerCall())
				} else {
					assert.Greater(t, tg.FlagsPerCall(), 1, "a bulk response evaluates every flag")
				}
			})
		}
	}
}
