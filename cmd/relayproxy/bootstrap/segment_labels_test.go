package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The series by segment name are opt-in and capped: off and 100 names by default, a cap below 1 is refused.
func TestMetricsSegmentLabels(t *testing.T) {
	t.Parallel()

	const base = "version: 2\nconnections:\n  c: {type: file, path: /x}\ncontrolPlane:\n  connection: c\n" +
		"  manifest: {key: m.yaml}\n"

	tests := []struct {
		name           string
		doc            string
		wantErr        string
		wantPerSegment bool
		wantMax        int32
	}{
		{name: "default", doc: base},
		{
			name:           "on with a cap",
			doc:            base + "observability:\n  metrics: {perSegmentLabels: true, maxSegmentLabels: 20}\n",
			wantPerSegment: true,
			wantMax:        20,
		},
		{
			name: "a negative cap", doc: base + "observability:\n  metrics: {maxSegmentLabels: -1}\n",
			wantErr: "observability.metrics.maxSegmentLabels (line 8): invalid value: expected at least 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tt.doc), nil, options(nil, nil, false))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPerSegment, cfg.Observability.Metrics.PerSegmentLabels)
			assert.Equal(t, tt.wantMax, cfg.Observability.Metrics.MaxSegmentLabels)
		})
	}
}
