package webhookexporter

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	hostnameKey         = "hostname"
	httpValidComWebhook = "http://valid.com/webhook"
)

type webhookExportFields struct {
	Meta        map[string]string
	Headers     map[string][]string
	EndpointURL string
	Secret      string
	httpClient  testhelpers.HTTPClientMock
}

type webhookExportArgs struct {
	logger        *slog.Logger
	featureEvents []exporter.ExportableEvent
}

type webhookExportExpected struct {
	headers      map[string][]string
	bodyFilePath string
	signHeader   string
}

type webhookExportCase struct {
	expected webhookExportExpected
	name     string
	args     webhookExportArgs
	fields   webhookExportFields
	wantErr  bool
}

func runWebhookExportTests(t *testing.T, tests []webhookExportCase) {
	t.Helper()

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			webhookExp := &Exporter{
				EndpointURL: tt.fields.EndpointURL,
				Secret:      tt.fields.Secret,
				Meta:        tt.fields.Meta,
				httpClient:  &tt.fields.httpClient,
				Headers:     tt.fields.Headers,
			}

			err := webhookExp.Export(context.Background(), tt.args.logger, tt.args.featureEvents)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)

			if tt.expected.bodyFilePath != "" {
				c, readFileErr := os.ReadFile(tt.expected.bodyFilePath)
				require.NoError(t, readFileErr)
				assert.JSONEq(t, string(c), tt.fields.httpClient.Body)
			}

			if tt.expected.signHeader != "" {
				assert.Equal(t, tt.expected.signHeader, tt.fields.httpClient.Signature)
			}

			if tt.expected.headers != nil {
				assert.Equal(t, tt.expected.headers, tt.fields.httpClient.Headers)
			}
		})
	}
}

func TestWebhook_IsBulk(t *testing.T) {
	t.Parallel()

	exp := Exporter{}
	assert.True(t, exp.IsBulk(), "DeprecatedExporterV1 is a bulk exporter")
}

func TestWebhook_Export(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	runWebhookExportTests(t, []webhookExportCase{
		{
			name: "Invalid EndpointURL",
			fields: webhookExportFields{
				EndpointURL: " http://invalid.com",
			},
			wantErr: true,
		},
		{
			name: "valid without signature",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testhelpers.HTTPClientMock{StatusCode: 200, ForceError: false},
				Meta:        map[string]string{hostnameKey: hostnameKey},
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyABCD,
						CreationDate: 1617970547,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        "YO",
						Default:      false,
						Source:       fixtures.SourceServer,
					},
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyEFGH,
						CreationDate: 1617970701,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        fixtures.ValueYO2,
						Default:      false,
						Version:      fixtures.VersionMajor127,
						Source:       fixtures.SourceServer,
					},
				},
			},
			expected: webhookExportExpected{
				bodyFilePath: "./testdata/valid_without_signature.json",
				signHeader:   "",
			},
			wantErr: false,
		},
		{
			name: "valid with signature",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testhelpers.HTTPClientMock{StatusCode: 200, ForceError: false},
				Meta:        map[string]string{hostnameKey: hostnameKey},
				Secret:      fixtures.SecretValue,
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyABCD,
						CreationDate: 1617970547,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        "YO",
						Default:      false,
						Source:       fixtures.SourceServer,
					},
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyEFGH,
						CreationDate: 1617970701,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        fixtures.ValueYO2,
						Default:      false,
						Version:      fixtures.VersionMajor127,
						Source:       fixtures.SourceServer,
					},
				},
			},
			expected: webhookExportExpected{
				bodyFilePath: "./testdata/valid_with_signature.json",
				signHeader:   "sha256=f1f9766836d4e513035986c035ee9f091b895709be47a802b5840467007e1ec0",
			},
			wantErr: false,
		},
	})
}

func TestWebhook_Export_ErrorsAndHeaders(t *testing.T) {
	t.Parallel()

	logger := slog.Default()
	runWebhookExportTests(t, []webhookExportCase{
		{
			name: "http error while calling webhook",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testhelpers.HTTPClientMock{StatusCode: 400, ForceError: false},
				Meta:        map[string]string{hostnameKey: hostnameKey},
				Secret:      fixtures.SecretValue,
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyABCD,
						CreationDate: 1617970547,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        "YO",
						Default:      false,
						Source:       fixtures.SourceServer,
					},
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyEFGH,
						CreationDate: 1617970701,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        fixtures.ValueYO2,
						Default:      false,
						Source:       fixtures.SourceServer,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "error in the during the Do call",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testhelpers.HTTPClientMock{StatusCode: 200, ForceError: true},
				Meta:        map[string]string{hostnameKey: hostnameKey},
				Secret:      fixtures.SecretValue,
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyABCD,
						CreationDate: 1617970547,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        "YO",
						Default:      false,
						Source:       fixtures.SourceServer,
					},
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyEFGH,
						CreationDate: 1617970701,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        fixtures.ValueYO2,
						Default:      false,
						Source:       fixtures.SourceServer,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "expect exporter to send custom headers",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testhelpers.HTTPClientMock{StatusCode: 200, ForceError: false},
				Meta:        map[string]string{hostnameKey: hostnameKey},
				Headers:     map[string][]string{"Authorization": {"Bearer auth_token"}},
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyABCD,
						CreationDate: 1617970547,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        "YO",
						Default:      false,
						Source:       fixtures.SourceServer,
					},
					&exporter.FeatureEvent{
						Kind:         fixtures.KindFeature,
						ContextKind:  fixtures.UserKeyAnonymous,
						UserKey:      fixtures.UserKeyEFGH,
						CreationDate: 1617970701,
						Key:          fixtures.UserKeyRandom,
						Variation:    fixtures.VariationDefault,
						Value:        fixtures.ValueYO2,
						Default:      false,
						Version:      fixtures.VersionMajor127,
						Source:       fixtures.SourceServer,
					},
				},
			},
			expected: webhookExportExpected{
				bodyFilePath: "./testdata/valid_without_signature.json",
				signHeader:   "",
				headers: map[string][]string{
					"Authorization": {"Bearer auth_token"},
					"Content-Type":  {"application/json"}},
			},
			wantErr: false,
		},
	})
}

func TestWebhook_Export_impossibleToParse(t *testing.T) {
	t.Parallel()

	f := &Exporter{
		EndpointURL: " http://invalid.com/",
	}

	err := f.Export(
		context.Background(),
		slog.Default(),
		[]exporter.ExportableEvent{},
	)
	assert.EqualError(
		t,
		err,
		"building the webhook request: parse \"[REDACTED]\": invalid URL",
	)
}
