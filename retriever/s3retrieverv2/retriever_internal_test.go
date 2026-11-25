package s3retrieverv2

// Whitebox because the real AWS clients built by Init are only reachable through the unexported
// downloader and lister fields. Not parallel because the subtests use t.Setenv.

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/retriever"
)

const (
	usWestTwo = "us-west-2"
)

// T.Setenv: the AWS SDK default config chain (third-party code) reads the AWS_* variables from the process environment.
func TestRetriever_Init_RealClients(t *testing.T) {
	useAccelerate := []func(*s3.Options){func(o *s3.Options) { o.UseAccelerate = true }}

	tests := []struct {
		r func() *Retriever
		// env sets extra environment variables for the test.
		env        map[string]string
		name       string
		wantRegion string
		awsConfig  bool // set an explicit AwsConfig in us-east-1
		wantLister bool
		// wantAccelerate is the expected UseAccelerate option of the built S3 client.
		wantAccelerate bool
		// nilCtx calls Init with a nil context.
		nilCtx bool
		// wantErr expects Init to fail while loading the default AWS configuration.
		wantErr bool
	}{
		{
			name:       "item mode with no AwsConfig",
			r:          func() *Retriever { return &Retriever{Bucket: "b", Item: "i"} },
			wantRegion: usWestTwo,
		},
		{
			name:       "prefix mode builds a lister",
			r:          func() *Retriever { return &Retriever{Bucket: "b", Prefix: "f/"} },
			wantRegion: usWestTwo,
			wantLister: true,
		},
		{
			name:       "with AwsConfig",
			r:          func() *Retriever { return &Retriever{Bucket: "b", Item: "i"} },
			awsConfig:  true,
			wantRegion: "us-east-1",
		},
		{
			name:           "with S3 client options",
			r:              func() *Retriever { return &Retriever{Bucket: "b", Prefix: "f/", S3ClientOptions: useAccelerate} },
			wantRegion:     usWestTwo,
			wantLister:     true,
			wantAccelerate: true,
		},
		{
			name:       "nil context",
			r:          func() *Retriever { return &Retriever{Bucket: "b", Item: "i"} },
			nilCtx:     true,
			wantRegion: usWestTwo,
		},
		{
			name:    "default AWS config cannot be loaded",
			r:       func() *Retriever { return &Retriever{Bucket: "b", Item: "i"} },
			env:     map[string]string{"AWS_PROFILE": "profile-that-does-not-exist"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_REGION", usWestTwo)

			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			r := tt.r()

			err := initRetriever(t, r, tt.awsConfig, tt.nilCtx)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, retriever.RetrieverError, r.Status())

				return
			}

			require.NoError(t, err)
			assert.NotNil(t, r.downloader)
			assert.Equal(t, tt.wantLister, r.lister != nil)
			assert.Equal(t, tt.wantRegion, r.AwsConfig.Region)
			assert.Equal(t, retriever.RetrieverReady, r.Status())
			assertListerClient(t, r, tt.wantLister, tt.wantAccelerate)
		})
	}
}

// initRetriever calls Init, optionally with an explicit us-east-1 AwsConfig or a nil context.
func initRetriever(t *testing.T, r *Retriever, awsConfig, nilCtx bool) error {
	t.Helper()

	if awsConfig {
		cfg, err := config.LoadDefaultConfig(t.Context(), config.WithRegion("us-east-1"))
		require.NoError(t, err)

		r.AwsConfig = &cfg
	}

	ctx := t.Context()
	if nilCtx {
		ctx = nil
	}

	return r.Init(ctx, nil)
}

// assertListerClient checks that the lister built by Init is a real S3 client with the expected
// UseAccelerate option.
func assertListerClient(t *testing.T, r *Retriever, wantLister, wantAccelerate bool) {
	t.Helper()

	if !wantLister {
		return
	}

	client, ok := r.lister.(*s3.Client)
	require.True(t, ok, "the lister should be a real S3 client")
	assert.Equal(t, wantAccelerate, client.Options().UseAccelerate)
}
