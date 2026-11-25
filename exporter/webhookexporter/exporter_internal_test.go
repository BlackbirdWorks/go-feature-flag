package webhookexporter

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testutils"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	aBCD                = "ABCD"
	anonymousUser       = "anonymousUser"
	eFGH                = "EFGH"
	feature             = "feature"
	fixture127          = "127"
	fixtureDefault      = "Default"
	hostname2           = "hostname"
	httpValidComWebhook = "http://valid.com/webhook"
	randomKey           = "random-key"
	sERVER              = "SERVER"
	secret              = "secret"
	yO2                 = "YO2"
)

type webhookExportFields struct {
	Meta        map[string]string
	Headers     map[string][]string
	EndpointURL string
	Secret      string
	httpClient  testutils.HTTPClientMock
}

type webhookExportArgs struct {
	logger        *fflog.FFLogger
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

	for _, tt := range tests {
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

	exporter := Exporter{}
	assert.True(t, exporter.IsBulk(), "DeprecatedExporterV1 is a bulk exporter")
}

func TestWebhook_Export(t *testing.T) {
	t.Parallel()

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}
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
				httpClient:  testutils.HTTPClientMock{StatusCode: 200, ForceError: false},
				Meta:        map[string]string{hostname2: hostname2},
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      aBCD,
						CreationDate: 1617970547,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        "YO",
						Default:      false,
						Source:       sERVER,
					},
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      eFGH,
						CreationDate: 1617970701,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        yO2,
						Default:      false,
						Version:      fixture127,
						Source:       sERVER,
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
				httpClient:  testutils.HTTPClientMock{StatusCode: 200, ForceError: false},
				Meta:        map[string]string{hostname2: hostname2},
				Secret:      secret,
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      aBCD,
						CreationDate: 1617970547,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        "YO",
						Default:      false,
						Source:       sERVER,
					},
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      eFGH,
						CreationDate: 1617970701,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        yO2,
						Default:      false,
						Version:      fixture127,
						Source:       sERVER,
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

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}
	runWebhookExportTests(t, []webhookExportCase{
		{
			name: "http error while calling webhook",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testutils.HTTPClientMock{StatusCode: 400, ForceError: false},
				Meta:        map[string]string{hostname2: hostname2},
				Secret:      secret,
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      aBCD,
						CreationDate: 1617970547,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        "YO",
						Default:      false,
						Source:       sERVER,
					},
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      eFGH,
						CreationDate: 1617970701,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        yO2,
						Default:      false,
						Source:       sERVER,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "error in the during the Do call",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testutils.HTTPClientMock{StatusCode: 200, ForceError: true},
				Meta:        map[string]string{hostname2: hostname2},
				Secret:      secret,
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      aBCD,
						CreationDate: 1617970547,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        "YO",
						Default:      false,
						Source:       sERVER,
					},
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      eFGH,
						CreationDate: 1617970701,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        yO2,
						Default:      false,
						Source:       sERVER,
					},
				},
			},
			wantErr: true,
		},
		{
			name: "expect exporter to send custom headers",
			fields: webhookExportFields{
				EndpointURL: httpValidComWebhook,
				httpClient:  testutils.HTTPClientMock{StatusCode: 200, ForceError: false},
				Meta:        map[string]string{hostname2: hostname2},
				Headers:     map[string][]string{"Authorization": {"Bearer auth_token"}},
			},
			args: webhookExportArgs{
				logger: logger,
				featureEvents: []exporter.ExportableEvent{
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      aBCD,
						CreationDate: 1617970547,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        "YO",
						Default:      false,
						Source:       sERVER,
					},
					exporter.FeatureEvent{
						Kind:         feature,
						ContextKind:  anonymousUser,
						UserKey:      eFGH,
						CreationDate: 1617970701,
						Key:          randomKey,
						Variation:    fixtureDefault,
						Value:        yO2,
						Default:      false,
						Version:      fixture127,
						Source:       sERVER,
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
		&fflog.FFLogger{LeveledLogger: slog.Default()},
		[]exporter.ExportableEvent{},
	)
	assert.EqualError(
		t,
		err,
		"building the webhook request: parse \"[REDACTED]\": invalid URL",
	)
}
