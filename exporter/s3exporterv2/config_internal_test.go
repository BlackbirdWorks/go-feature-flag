package s3exporterv2

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const regionChain = "from-the-chain"

const regionConnection = "from-the-connection"

const kindFeature = "feature"

var errNoChain = errors.New("the default chain is down")

// TestExporter_ConfigurationSource pins where the AWS configuration comes from: a configuration that is given (the
// relay proxy passes the one of the connection factory) is used as it is and the default chain is never loaded;
// without one, a library user keeps the default chain.
func TestExporter_ConfigurationSource(t *testing.T) {
	t.Parallel()

	given := &aws.Config{Region: regionConnection}

	tests := []struct {
		given       *aws.Config
		loadErr     error
		name        string
		wantRegion  string
		wantErr     string
		wantLoads   int32
		wantGivenAs bool
	}{
		{
			name: "a given configuration is used and the default chain is not loaded", given: given,
			wantRegion: regionConnection, wantLoads: 0, wantGivenAs: true,
		},
		{
			name: "without a configuration the default chain is loaded once", wantRegion: regionChain,
			wantLoads: 1,
		},
		{
			name:      "a failing default chain is reported",
			loadErr:   errNoChain,
			wantErr:   "impossible to init S3 exporter",
			wantLoads: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var loads atomic.Int32

			exp := newConfigTestExporter(tt.given, func(context.Context) (aws.Config, error) {
				loads.Add(1)

				return aws.Config{Region: regionChain}, tt.loadErr
			})

			err := exp.initializeUploader(t.Context())

			assert.Equal(t, tt.wantLoads, loads.Load())

			if tt.wantErr != "" {
				require.ErrorIs(t, err, errNoChain)
				assert.ErrorContains(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			require.NotNil(t, exp.AwsConfig)
			assert.Equal(t, tt.wantRegion, exp.AwsConfig.Region)
			assert.Equal(t, tt.wantGivenAs, exp.AwsConfig == tt.given, "the very configuration that was given")
		})
	}
}

func newConfigTestExporter(given *aws.Config, load func(context.Context) (aws.Config, error)) *Exporter {
	return &Exporter{AwsConfig: given, loadConfig: load}
}

// TestExporter_GivenClientNeverLoadsAConfiguration pins the path of the relay proxy: the S3 client of the
// connection is used as it is, with neither a configuration nor the default chain.
func TestExporter_GivenClientNeverLoadsAConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		config        *aws.Config
		name          string
		wantLoads     int32
		wantConfigNil bool
	}{
		{name: "a client alone", wantLoads: 0, wantConfigNil: true},
		{
			name: "a client wins over a configuration", config: &aws.Config{Region: regionConnection},
			wantLoads: 0, wantConfigNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var loads atomic.Int32

			exp := &Exporter{
				S3Client: s3.New(s3.Options{Region: regionConnection}), AwsConfig: tt.config,
				loadConfig: func(context.Context) (aws.Config, error) {
					loads.Add(1)

					return aws.Config{}, errNoChain
				},
			}

			require.NoError(t, exp.initializeUploader(t.Context()))

			assert.Equal(t, tt.wantLoads, loads.Load(), "the default chain is not loaded")
			assert.Equal(t, tt.wantConfigNil, exp.AwsConfig == nil)
			assert.NotNil(t, exp.s3Uploader)
		})
	}
}
