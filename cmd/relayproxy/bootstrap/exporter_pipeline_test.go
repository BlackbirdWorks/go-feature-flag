package bootstrap_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const pathInitialBackoff = "exporters.e.retry.initialBackoff"

const pipelineDoc = "version: 2\nexporters:\n  e: {kind: log, "

// TestExporterPipeline: the settings of the event pipeline of an exporter take their defaults, and a value that
// the pipeline cannot run with is refused at its path.
func TestExporterPipeline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr      error
		name         string
		doc          string
		wantPath     string
		wantPolicy   string
		wantExhaust  string
		wantBlock    time.Duration
		wantDrain    time.Duration
		wantMaxBytes bootstrap.ByteSize
		wantAttempts int
	}{
		{
			name: "defaults", doc: pipelineDoc + "flushInterval: 5s}\n", wantPolicy: bootstrap.OverflowDropNewest,
			wantBlock: bootstrap.DefaultBlockTimeout, wantDrain: bootstrap.DefaultExporterDrainTimeout,
			wantAttempts: bootstrap.DefaultRetryAttempts,
			wantExhaust:  bootstrap.RetryKeep,
		},
		{
			name: "all set", wantPolicy: bootstrap.OverflowBlock, wantBlock: time.Second, wantDrain: 3 * time.Second,
			doc: pipelineDoc + "overflowPolicy: block, blockTimeout: 1s, drainTimeout: 3s, maxBytes: 64KiB, " +
				"retry: {maxAttempts: 2, initialBackoff: 1s, maxBackoff: 2s, onExhausted: drop}}\n",
			wantMaxBytes: 64 << 10, wantAttempts: 2, wantExhaust: bootstrap.RetryDrop,
		},
		{
			name: "unknown policy", doc: pipelineDoc + "overflowPolicy: dropAll}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.overflowPolicy",
		},
		{
			name: "block wait too long", doc: pipelineDoc + "overflowPolicy: block, blockTimeout: 11s}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.blockTimeout",
		},
		{
			name: "drain too long", doc: pipelineDoc + "drainTimeout: 11m}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.drainTimeout",
		},
		{
			name: "no attempt", doc: pipelineDoc + "retry: {maxAttempts: 0}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.retry.maxAttempts",
		},
		{
			name: "too many attempts", doc: pipelineDoc + "retry: {maxAttempts: 21}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.retry.maxAttempts",
		},
		{
			name: "backoff inverted", doc: pipelineDoc + "retry: {initialBackoff: 10s, maxBackoff: 1s}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: pathInitialBackoff,
		},
		{
			name: "unknown retry key", doc: pipelineDoc + "retry: {attempts: 2}}\n",
			wantErr: bootstrap.ErrUnknownKey, wantPath: "exporters.e.retry.attempts",
		},
		{
			name: "unknown exhaustion", doc: pipelineDoc + "retry: {onExhausted: stop}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.retry.onExhausted",
		},
		{
			name: "batch above the bound", doc: pipelineDoc + "maxBytes: 2GiB}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.maxBytes",
		},
		{
			name:     "initial backoff above the bound",
			doc:      pipelineDoc + "retry: {initialBackoff: 11m, maxBackoff: 20m}}\n",
			wantErr:  bootstrap.ErrInvalidValue,
			wantPath: pathInitialBackoff,
		},
		{
			name: "max backoff above the bound", doc: pipelineDoc + "retry: {maxBackoff: 11m}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.retry.maxBackoff",
		},
		{
			name: "max elapsed above the bound", doc: pipelineDoc + "retry: {maxElapsed: 61m}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: "exporters.e.retry.maxElapsed",
		},
		{
			name: "initial backoff above the default max backoff", doc: pipelineDoc + "retry: {initialBackoff: 10s}}\n",
			wantErr: bootstrap.ErrInvalidValue, wantPath: pathInitialBackoff,
		},
		{
			name: "bare number size", doc: pipelineDoc + "maxBytes: 1024}\n",
			wantErr: bootstrap.ErrInvalidByteSize, wantPath: "exporters.e.maxBytes",
		},
	}

	for _, tcase := range tests {
		t.Run(tcase.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tcase.doc), nil, bootstrap.LoadOptions{})
			if tcase.wantErr != nil {
				require.ErrorIs(t, err, tcase.wantErr)

				var paths []string
				for _, perr := range pathErrors(err) {
					paths = append(paths, perr.Path)
				}

				assert.Contains(t, paths, tcase.wantPath)

				return
			}

			require.NoError(t, err)

			exp := cfg.Exporters["e"]
			assert.Equal(t, tcase.wantPolicy, exp.OverflowPolicy)
			assert.Equal(t, tcase.wantBlock, time.Duration(*exp.BlockTimeout))
			assert.Equal(t, tcase.wantDrain, time.Duration(*exp.DrainTimeout))
			assert.Equal(t, tcase.wantAttempts, *exp.Retry.MaxAttempts)
			assert.Equal(t, tcase.wantExhaust, exp.Retry.OnExhausted)

			if tcase.wantMaxBytes != 0 {
				assert.Equal(t, tcase.wantMaxBytes, *exp.MaxBytes)
			}
		})
	}
}
