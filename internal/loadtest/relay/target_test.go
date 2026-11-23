package relay_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thomaspoignant/go-feature-flag/internal/loadtest"
	"github.com/thomaspoignant/go-feature-flag/internal/loadtest/relay"
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
		{relay.GOFFEval, "/v1/feature/k/eval", true},
		{relay.GOFFAllFlags, "/v1/allflags", false},
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
		{"unknown exporter", relay.GOFFEval, loadtest.ExporterMode("flaky")},
		{"slow exporter on goff bulk", relay.GOFFAllFlags, loadtest.ExporterSlow},
		{"failing exporter on goff bulk", relay.GOFFAllFlags, loadtest.ExporterFailing},
		{"slow exporter on ofrep bulk", relay.OFREPBulk, loadtest.ExporterSlow},
		{"failing exporter on ofrep bulk", relay.OFREPBulk, loadtest.ExporterFailing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tg, err := relay.NewTarget(relay.Options{FlagsYAML: loadtest.FlagsYAML(), ExporterMode: tt.mode},
				tt.ep, "rule-bool-track", 1)
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
		{"goff known flag", relay.GOFFEval, loadtest.ExporterDiscard, "rule-bool-track", false},
		{"goff unknown flag is an error", relay.GOFFEval, loadtest.ExporterDiscard, "does-not-exist", true},
		{"ofrep known flag", relay.OFREPEval, loadtest.ExporterDiscard, "rule-bool-track", false},
		{"ofrep unknown flag is an error", relay.OFREPEval, loadtest.ExporterDiscard, "does-not-exist", true},
		{"goff bulk with discard exporter", relay.GOFFAllFlags, loadtest.ExporterDiscard, "rule-bool-track", false},
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
	r, err := relay.Start(relay.Options{FlagsYAML: "{{{ not yaml"})
	require.Error(t, err)
	assert.Nil(t, r)
}
