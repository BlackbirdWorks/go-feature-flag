package manifestreader_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

func TestNew_ConfigValidation(t *testing.T) {
	t.Parallel()

	base := manifestreader.Config{ManifestKey: "m", Connection: "c"}

	with := func(mutate func(*manifestreader.Config)) manifestreader.Config {
		cfg := base
		mutate(&cfg)

		return cfg
	}

	tests := []struct {
		name    string
		cfg     manifestreader.Config
		wantErr bool
	}{
		{name: "key and connection are enough", cfg: base},
		{name: "no manifest key", cfg: with(func(c *manifestreader.Config) { c.ManifestKey = "" }), wantErr: true},
		{
			name:    "a manifest key with a leading slash",
			cfg:     with(func(c *manifestreader.Config) { c.ManifestKey = "/m" }),
			wantErr: true,
		},
		{
			name: "a manifest key with a prefix path",
			cfg:  with(func(c *manifestreader.Config) { c.ManifestKey = "a/b/m" }),
		},
		{name: "no connection", cfg: with(func(c *manifestreader.Config) { c.Connection = "" }), wantErr: true},
		{name: "negative interval", cfg: with(func(c *manifestreader.Config) { c.PollInterval = -1 }), wantErr: true},
		{
			name:    "interval below the minimum",
			cfg:     with(func(c *manifestreader.Config) { c.PollInterval = 999 * time.Millisecond }),
			wantErr: true,
		},
		{name: "interval at the minimum", cfg: with(func(c *manifestreader.Config) { c.PollInterval = time.Second })},
		{
			name:    "negative request timeout",
			cfg:     with(func(c *manifestreader.Config) { c.RequestTimeout = -1 }),
			wantErr: true,
		},
		{name: "negative max objects", cfg: with(func(c *manifestreader.Config) { c.MaxObjects = -1 }), wantErr: true},
		{
			name:    "negative manifest bytes",
			cfg:     with(func(c *manifestreader.Config) { c.MaxManifestBytes = -1 }),
			wantErr: true,
		},
		{
			name:    "negative object bytes",
			cfg:     with(func(c *manifestreader.Config) { c.MaxObjectBytes = -1 }),
			wantErr: true,
		},
		{
			name:    "negative total bytes",
			cfg:     with(func(c *manifestreader.Config) { c.MaxTotalBytes = -1 }),
			wantErr: true,
		},
		{
			name:    "negative concurrency",
			cfg:     with(func(c *manifestreader.Config) { c.FetchConcurrency = -1 }),
			wantErr: true,
		},
		{
			name:    "concurrency above the cap",
			cfg:     with(func(c *manifestreader.Config) { c.FetchConcurrency = 17 }),
			wantErr: true,
		},
		{name: "concurrency at the cap", cfg: with(func(c *manifestreader.Config) { c.FetchConcurrency = 16 })},
		{name: "jitter of 1", cfg: with(func(c *manifestreader.Config) { c.JitterFraction = 1 }), wantErr: true},
		{name: "jitter above 1", cfg: with(func(c *manifestreader.Config) { c.JitterFraction = 1.5 }), wantErr: true},
		{name: "negative jitter", cfg: with(func(c *manifestreader.Config) { c.JitterFraction = -0.1 }), wantErr: true},
		{name: "jitter inside [0, 1)", cfg: with(func(c *manifestreader.Config) { c.JitterFraction = 0.99 })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := tt.cfg

			reader, err := manifestreader.New(newFakeStore(), &tt.cfg)

			assert.Equal(t, before, tt.cfg, "the caller's config is never changed")

			if tt.wantErr {
				require.ErrorIs(t, err, manifestreader.ErrInvalidConfig)
				assert.Nil(t, reader)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, manifestreader.PollNone, reader.Status().Poll.Result)
			assert.Equal(t, manifestreader.OutcomeNone, reader.Status().Manifest.Outcome)
		})
	}
}

func TestNew_NilConfigIsRefusedForWantOfAKey(t *testing.T) {
	t.Parallel()

	reader, err := manifestreader.New(newFakeStore(), nil)

	require.ErrorIs(t, err, manifestreader.ErrInvalidConfig)
	assert.Nil(t, reader)
}

func TestNew_DefaultsAreTheDocumentedOnes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 60*time.Second, manifestreader.DefaultPollInterval)
	assert.Equal(t, 60*time.Second, manifestreader.DefaultRequestTimeout)
	assert.Equal(t, int64(16<<20), int64(manifestreader.DefaultMaxManifestBytes))
	assert.Equal(t, int64(1<<30), int64(manifestreader.DefaultMaxTotalBytes))
}
