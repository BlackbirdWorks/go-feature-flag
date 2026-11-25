package ffclient_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffClient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/retriever/githubretriever"
	"github.com/blackbirdworks/semaphore/retriever/gitlabretriever"
	"github.com/blackbirdworks/semaphore/retriever/httpretriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

// contextKey is the type of the keys of the values the tests put in a context.
type contextKey string

type contextRecordingRetriever struct {
	initVal        any
	retrieveVal    any
	shutdownVal    any
	key            contextKey
	initNonNil     bool
	retrieveNonNil bool
	shutdownNonNil bool
}

var _ retriever.InitializableRetriever = (*contextRecordingRetriever)(nil)

func (r *contextRecordingRetriever) Retrieve(ctx context.Context) ([]byte, error) {
	r.retrieveNonNil = ctx != nil
	if ctx != nil {
		r.retrieveVal = ctx.Value(r.key)
	}

	return []byte("flag:\n  variations:\n    on: true\n    off: false\n  defaultRule:\n    variation: on\n"), nil
}

func (r *contextRecordingRetriever) Init(ctx context.Context, _ *fflog.FFLogger) error {
	r.initNonNil = ctx != nil
	if ctx != nil {
		r.initVal = ctx.Value(r.key)
	}

	return nil
}

func (r *contextRecordingRetriever) Shutdown(ctx context.Context) error {
	r.shutdownNonNil = ctx != nil
	if ctx != nil {
		r.shutdownVal = ctx.Value(r.key)
	}

	return nil
}

func (r *contextRecordingRetriever) Status() retriever.Status {
	return retriever.RetrieverReady
}

func TestConfig_Initialize(t *testing.T) {
	t.Parallel()

	t.Run("adjust polling interval", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name            string
			pollingInterval time.Duration
			want            time.Duration
		}{
			{
				name:            "empty",
				pollingInterval: 0,
				want:            60 * time.Second,
			},
			{
				name:            "lower than minimum",
				pollingInterval: 1 * time.Millisecond,
				want:            1 * time.Second,
			},
			{
				name:            "valid",
				pollingInterval: 42 * time.Minute,
				want:            42 * time.Minute,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				c := ffClient.Config{
					PollingInterval: tt.pollingInterval,
				}
				c.Initialize()

				assert.Equal(t, tt.want, c.PollingInterval)
			})
		}
	})
}

func TestConfig_GetRetrievers(t *testing.T) {
	t.Parallel()

	type fields struct {
		Retriever       retriever.Retriever
		PollingInterval time.Duration
	}

	tests := []struct {
		name    string
		fields  fields
		want    string
		wantErr bool
	}{
		{
			name: "File retriever",
			fields: fields{
				PollingInterval: 3 * time.Second,
				Retriever:       &fileretriever.Retriever{Path: "file-example.yaml"},
			},
			want:    "*fileretriever.Retriever",
			wantErr: false,
		},
		{
			name: "S3 retriever",
			fields: fields{
				PollingInterval: 3 * time.Second,
				Retriever: &s3retrieverv2.Retriever{
					Bucket: "tpoi-test",
					Item:   "flag-config.yaml",
				},
			},
			want:    "*s3retrieverv2.Retriever",
			wantErr: false,
		},
		{
			name: "HTTP retriever",
			fields: fields{
				PollingInterval: 3 * time.Second,
				Retriever: &httpretriever.Retriever{
					URL:    "http://example.com/flag-config.yaml",
					Method: http.MethodGet,
				},
			},
			want:    "*httpretriever.Retriever",
			wantErr: false,
		},
		{
			name: "Github retriever",
			fields: fields{
				PollingInterval: 3 * time.Second,
				Retriever: &githubretriever.Retriever{
					RepositorySlug: "thomaspoignant/go-feature-flag",
					FilePath:       "testdata/flag-config.yaml",
					GithubToken:    "XXX",
				},
			},
			want:    "*githubretriever.Retriever",
			wantErr: false,
		},
		{
			name: "Gitlab retriever",
			fields: fields{
				PollingInterval: 3 * time.Second,
				Retriever: &gitlabretriever.Retriever{
					BaseURL:        "https://gitlab.com/",
					RepositorySlug: "ruairi2/go-feature-flags-config",
					FilePath:       "flag-config.yaml",
					GitlabToken:    "XXX",
				},
			},
			want:    "*gitlabretriever.Retriever",
			wantErr: false,
		},
		{
			name: "No retriever",
			fields: fields{
				PollingInterval: 3 * time.Second,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &ffClient.Config{
				PollingInterval: tt.fields.PollingInterval,
				Retriever:       tt.fields.Retriever,
			}
			got, err := c.GetRetrievers()
			assert.Equal(t, tt.wantErr, err != nil)

			if err == nil {
				assert.Equal(t, tt.want, reflect.ValueOf(got[0]).Type().String())
			}
		})
	}
}

func TestOfflineConfig(t *testing.T) {
	t.Parallel()

	c := ffClient.Config{
		Offline: true,
	}
	assert.True(t, c.IsOffline())
	c.SetOffline(false)
	assert.False(t, c.IsOffline())
}

func TestConfig_GetDataExporters(t *testing.T) {
	t.Parallel()

	type fields struct {
		DataExporters []ffClient.DataExporter
		DataExporter  ffClient.DataExporter
	}

	tests := []struct {
		name   string
		want   []ffClient.DataExporter
		fields fields
	}{
		{
			name:   "No data exporter",
			fields: fields{},
			want:   []ffClient.DataExporter{},
		},
		{
			name: "Single data exporter",
			fields: fields{
				DataExporter: ffClient.DataExporter{
					FlushInterval:    10 * time.Second,
					MaxEventInMemory: 100,
				},
			},
			want: []ffClient.DataExporter{
				{
					FlushInterval:    10 * time.Second,
					MaxEventInMemory: 100,
				},
			},
		},
		{
			name: "Multiple data exporters",
			fields: fields{
				DataExporters: []ffClient.DataExporter{
					{
						FlushInterval:    20 * time.Second,
						MaxEventInMemory: 200,
					},
					{
						FlushInterval:    30 * time.Second,
						MaxEventInMemory: 300,
					},
				},
			},
			want: []ffClient.DataExporter{
				{
					FlushInterval:    20 * time.Second,
					MaxEventInMemory: 200,
				},
				{
					FlushInterval:    30 * time.Second,
					MaxEventInMemory: 300,
				},
			},
		},
		{
			name: "Both single and multiple data exporters",
			fields: fields{
				DataExporter: ffClient.DataExporter{
					FlushInterval:    10 * time.Second,
					MaxEventInMemory: 100,
				},
				DataExporters: []ffClient.DataExporter{
					{
						FlushInterval:    20 * time.Second,
						MaxEventInMemory: 200,
					},
					{
						FlushInterval:    30 * time.Second,
						MaxEventInMemory: 300,
					},
				},
			},
			want: []ffClient.DataExporter{
				{
					FlushInterval:    10 * time.Second,
					MaxEventInMemory: 100,
				},
				{
					FlushInterval:    20 * time.Second,
					MaxEventInMemory: 200,
				},
				{
					FlushInterval:    30 * time.Second,
					MaxEventInMemory: 300,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &ffClient.Config{
				DataExporter:  tt.fields.DataExporter,
				DataExporters: tt.fields.DataExporters,
			}
			got := c.GetDataExporters()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewWithContext(t *testing.T) {
	t.Parallel()

	const key = contextKey("trace-id")

	tests := []struct {
		wantInitVal     any
		wantRetrieveVal any
		wantShutdownVal any
		name            string
		initVal         string
		closeVal        string
		nilCtx          bool
	}{
		{
			name:            "custom context propagates to retriever lifecycle",
			initVal:         "init-123",
			closeVal:        "close-456",
			wantInitVal:     "init-123",
			wantRetrieveVal: "init-123",
			wantShutdownVal: "close-456",
		},
		{
			name:   "nil context falls back to background context",
			nilCtx: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &contextRecordingRetriever{key: key}

			var initCtx, closeCtx context.Context
			if !tt.nilCtx {
				initCtx = context.WithValue(t.Context(), key, tt.initVal)
				closeCtx = context.WithValue(t.Context(), key, tt.closeVal)
			}

			client, err := ffClient.NewWithContext(initCtx, ffClient.Config{
				PollingInterval: 10 * time.Second,
				Retriever:       rec,
			})
			require.NoError(t, err)
			client.CloseWithContext(closeCtx)

			assert.True(t, rec.initNonNil)
			assert.True(t, rec.retrieveNonNil)
			assert.True(t, rec.shutdownNonNil)
			assert.Equal(t, tt.wantInitVal, rec.initVal)
			assert.Equal(t, tt.wantRetrieveVal, rec.retrieveVal)
			assert.Equal(t, tt.wantShutdownVal, rec.shutdownVal)
		})
	}
}
