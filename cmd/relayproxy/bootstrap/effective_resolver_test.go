package bootstrap_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

func TestConfig_EffectiveResolver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		doc           string
		wantNormalize string
		wantMaxID     int
	}{
		{
			name: "no segments section: the defaults of the relay", doc: "version: 2\n",
			wantNormalize: bootstrap.NormalizeNone, wantMaxID: bootstrap.DefaultSegmentMaxIDBytes,
		},
		{
			name:          "a segments section without the settings",
			doc:           "version: 2\nsegments:\n  resolver: {}\n",
			wantNormalize: bootstrap.NormalizeNone, wantMaxID: bootstrap.DefaultSegmentMaxIDBytes,
		},
		{
			name:          "the settings of the file",
			doc:           "version: 2\nsegments:\n  resolver:\n    maxIdBytes: 40\n    default: {normalize: trimLower}\n",
			wantNormalize: bootstrap.NormalizeTrimLower,
			wantMaxID:     40,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tt.doc), nil, bootstrap.LoadOptions{})
			require.NoError(t, err)

			got := cfg.EffectiveResolver()
			assert.Equal(t, tt.wantNormalize, got.Default.Normalize)
			require.NotNil(t, got.MaxIDBytes)
			assert.Equal(t, tt.wantMaxID, *got.MaxIDBytes)
		})
	}
}
