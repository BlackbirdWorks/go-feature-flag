package ofrep_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	failedToCreateFlagset   = "failed to create flagset manager"
	numberFlag              = "number-flag"
	ofrepInvalidContextJSON = "/ofrep/invalid_context.json"
	ofrepNoTargetingKey     = "/ofrep/no_targeting_key_context.json"
	ofrepEvaluatePrefix     = "/ofrep/v1/evaluate/flags/"
	ofrepValidRequestJSON   = "/ofrep/valid_request.json"
	postOfrepVOneEvaluate   = "POST /ofrep/v1/evaluate/flags/{flagKey...}"
	sse                     = "sse"
	targetingKeyRule        = "targeting-key-rule"
)

const (
	testdataDir         = "../../testdata"
	configFlagsLocation = testdataDir + "/semaphore/config_flags.yaml"
)

func Test_Bulk_Evaluation(t *testing.T) {
	t.Parallel()

	type want struct {
		bodyFile   string
		errorMsg   string
		httpCode   int
		errorCode  int
		handlerErr bool
	}

	type args struct {
		bodyFile            string
		configFlagsLocation string
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "valid flag",
			args: args{
				bodyFile:            testdataDir + ofrepValidRequestJSON,
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/ofrep/responses/valid_response.json",
			},
		},
		{
			name: "specify flag list in context",
			args: args{
				bodyFile:            testdataDir + "/ofrep/valid_request_specify_flags.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/ofrep/responses/valid_response_specify_flags.json",
			},
		},
		{
			name: "Invalid context",
			args: args{
				bodyFile:            testdataDir + ofrepInvalidContextJSON,
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusBadRequest,
				bodyFile: testdataDir + "/ofrep/responses/invalid_context.json",
			},
		},
		{
			name: "Empty body",
			args: args{
				bodyFile:            testdataDir + "/ofrep/empty_body.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusBadRequest,
				bodyFile: testdataDir + "/ofrep/responses/empty_body.json",
			},
		},
		{
			name: "Nil context",
			args: args{
				bodyFile:            testdataDir + "/ofrep/nil_context.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusBadRequest,
				bodyFile: testdataDir + "/ofrep/responses/nil_context.json",
			},
		},
		{
			name: "No Targeting Key in context",
			// in this case we don't have a targetingKey, so we will evaluate the flags individually
			// if the flag requires bucketing, we will return a targeting key missing error
			args: args{
				bodyFile:            testdataDir + ofrepNoTargetingKey,
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/ofrep/responses/no_targeting_key_context.json",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create flagset manager with configuration
			cfg := mock.FileConfig(tt.args.configFlagsLocation)
			flagsetManager := mock.NewDefault(t, &cfg)

			defer flagsetManager.Close()

			ctrl := ofrep.NewOFREPEvaluate(flagsetManager, &metric.Metrics{}, ofrep.EventStream{})
			rec := httptest.NewRecorder()

			// read wantBody request file
			var bodyReq io.Reader

			if tt.args.bodyFile != "" {
				bodyReqContent, readFileErr := os.ReadFile(tt.args.bodyFile)
				require.NoError(t, readFileErr, "request wantBody file missing %s", tt.args.bodyFile)

				bodyReq = strings.NewReader(string(bodyReqContent))
			}

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				fixtures.OFREPEvaluateFlagsPath,
				bodyReq,
			)
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			handlerErr := ctrl.BulkEvaluate(rec, req)

			if tt.want.handlerErr {
				require.Error(t, handlerErr, "handler should return an error")

				he := &helper.HTTPError{}

				ok := errors.As(handlerErr, &he)
				if ok {
					assert.Equal(t, tt.want.errorCode, he.Code)
					assert.Equal(t, tt.want.errorMsg, he.Message)
				} else {
					assert.Equal(t, tt.want.errorMsg, handlerErr.Error())
				}

				return
			}

			wantBody, err := os.ReadFile(tt.want.bodyFile)

			require.NoError(t, err, "Impossible the expected wantBody file %s", tt.want.bodyFile)
			assert.Equal(t, tt.want.httpCode, rec.Code, "Invalid HTTP Code")
			assert.JSONEq(t, string(wantBody), rec.Body.String(), "Invalid response wantBody")
		})
	}
}

func Test_Bulk_Evaluation_EventStreams(t *testing.T) {
	t.Parallel()

	type args struct {
		headers     map[string]string
		eventStream ofrep.EventStream
	}

	tests := []struct {
		name string
		args args
		want []model.OFREPEventStream
	}{
		{
			name: "disabled when no base URL is configured",
			args: args{},
			want: nil,
		},
		{
			name: "base URL and inactivity delay from the configuration",
			args: args{
				eventStream: mustEventStream(t, "https://gofeatureflag.example.com/", 60),
			},
			want: []model.OFREPEventStream{
				{
					Type:               sse,
					URL:                "https://gofeatureflag.example.com/stream/v1/sse/flag/change",
					InactivityDelaySec: 60,
				},
			},
		},
		{
			name: "base URL with a path prefix",
			args: args{
				eventStream: mustEventStream(t, "https://example.com/goff", 0),
			},
			want: []model.OFREPEventStream{
				{Type: sse, URL: "https://example.com/goff/stream/v1/sse/flag/change"},
			},
		},
		{
			name: "API key from the X-API-Key header",
			args: args{
				eventStream: mustEventStream(t, "https://example.com", 0),
				headers:     map[string]string{"X-API-Key": "my-key"},
			},
			want: []model.OFREPEventStream{
				{Type: sse, URL: "https://example.com/stream/v1/sse/flag/change?apiKey=my-key"},
			},
		},
		{
			name: "API key from the Authorization header is escaped",
			args: args{
				eventStream: mustEventStream(t, "https://example.com", 0),
				headers:     map[string]string{"Authorization": "Bearer my key&x=1"},
			},
			want: []model.OFREPEventStream{
				{Type: sse, URL: "https://example.com/stream/v1/sse/flag/change?apiKey=my+key%26x%3D1"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := mock.FileConfig(configFlagsLocation)
			flagsetManager := mock.NewDefault(t, &cfg)

			defer flagsetManager.Close()

			ctrl := ofrep.NewOFREPEvaluate(flagsetManager, &metric.Metrics{}, tt.args.eventStream)
			body, err := os.ReadFile(testdataDir + ofrepValidRequestJSON)
			require.NoError(t, err)

			req := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				fixtures.OFREPEvaluateFlagsPath,
				strings.NewReader(string(body)),
			)
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

			for k, v := range tt.args.headers {
				req.Header.Set(k, v)
			}

			rec := httptest.NewRecorder()

			require.NoError(t, ctrl.BulkEvaluate(rec, req))
			assert.Equal(t, http.StatusOK, rec.Code)

			var got model.OFREPBulkEvaluateSuccessResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Equal(t, tt.want, got.EventStreams)
		})
	}
}

func Test_Evaluate(t *testing.T) {
	t.Parallel()

	type want struct {
		bodyFile string
		httpCode int
	}

	type args struct {
		bodyFile            string
		configFlagsLocation string
		flagKey             string
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: "flag key with an escaped reserved character",
			args: args{
				bodyFile:            testdataDir + ofrepValidRequestJSON,
				configFlagsLocation: testdataDir + "/ofrep/config_flags_special_chars.yaml",
				flagKey:             "team%3Acheckout",
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/ofrep/responses/valid_evaluation_special_chars.json",
			},
		},
		{
			name: "valid evaluation",
			args: args{
				bodyFile:            testdataDir + ofrepValidRequestJSON,
				configFlagsLocation: configFlagsLocation,
				flagKey:             numberFlag,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/ofrep/responses/valid_evaluation.json",
			},
		},
		{
			name: "Invalid context",
			args: args{
				bodyFile:            testdataDir + ofrepInvalidContextJSON,
				configFlagsLocation: configFlagsLocation,
				flagKey:             numberFlag,
			},
			want: want{
				httpCode: http.StatusBadRequest,
				bodyFile: testdataDir + "/ofrep/responses/invalid_context_with_key.json",
			},
		},
		{
			name: "Nil context",
			args: args{
				bodyFile:            testdataDir + "/ofrep/nil_context.json",
				configFlagsLocation: configFlagsLocation,
				flagKey:             numberFlag,
			},
			want: want{
				httpCode: http.StatusBadRequest,
				bodyFile: testdataDir + "/ofrep/responses/nil_context_with_key.json",
			},
		},
		{
			name: "No Targeting Key for bucketing-required flag - should return 400 from core evaluation",
			args: args{
				bodyFile:            testdataDir + ofrepNoTargetingKey,
				configFlagsLocation: configFlagsLocation,
				flagKey:             numberFlag, // This flag has percentage rules, requires bucketing
			},
			want: want{
				httpCode: http.StatusBadRequest,
				bodyFile: testdataDir + "/ofrep/responses/no_targeting_key_bucketing_flag.json",
			},
		},
		{
			name: "No Targeting Key for non-bucketing flag - should succeed",
			args: args{
				bodyFile:            testdataDir + ofrepNoTargetingKey,
				configFlagsLocation: configFlagsLocation,
				flagKey:             targetingKeyRule, // This flag has no percentages, doesn't require bucketing
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/ofrep/responses/no_targeting_key_static_flag.json",
			},
		},
		{
			name: "Percentage-based rule in flag without targeting key should return 400 error",
			args: args{
				bodyFile:            testdataDir + ofrepNoTargetingKey,
				configFlagsLocation: configFlagsLocation,
				flagKey:             "flag-only-for-admin", // This flag has percentage rules, requires bucketing
			},
			want: want{
				httpCode: http.StatusBadRequest, // Core evaluation returns 400 for missing targeting key
				bodyFile: testdataDir + "/ofrep/responses/percentage_flag_no_key_error.json",
			},
		},
		{
			name: "Empty flag key",
			args: args{
				bodyFile:            testdataDir + ofrepValidRequestJSON,
				configFlagsLocation: configFlagsLocation,
				flagKey:             "",
			},
			want: want{
				// the router answers 404 before the handler when the key is missing, the handler still
				// refuses an empty key.
				httpCode: http.StatusBadRequest,
				bodyFile: testdataDir + "/ofrep/responses/empty_key.json",
			},
		},
		{
			name: "targeting using the field targetingKey in the rules",
			args: args{
				bodyFile:            testdataDir + "/ofrep/valid_targeting_key_query_request.json",
				configFlagsLocation: configFlagsLocation,
				flagKey:             targetingKeyRule,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/ofrep/responses/valid_targeting_key_query_response.json",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create flagset manager with configuration
			cfg := mock.FileConfig(tt.args.configFlagsLocation)
			flagsetManager := mock.NewDefault(t, &cfg)

			defer flagsetManager.Close()

			ctrl := ofrep.NewOFREPEvaluate(flagsetManager, &metric.Metrics{}, ofrep.EventStream{})
			mux := http.NewServeMux()
			mux.Handle(postOfrepVOneEvaluate, helper.Handle(ctrl.Evaluate, false))

			rec := httptest.NewRecorder()

			flagKey := tt.args.flagKey

			// read wantBody request file
			var bodyReq io.Reader

			if tt.args.bodyFile != "" {
				bodyReqContent, readFileErr := os.ReadFile(tt.args.bodyFile)
				require.NoError(t, readFileErr, "request wantBody file missing %s", tt.args.bodyFile)

				bodyReq = strings.NewReader(string(bodyReqContent))
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, ofrepEvaluatePrefix+flagKey, bodyReq)
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

			mux.ServeHTTP(rec, req)

			wantBody, err := os.ReadFile(tt.want.bodyFile)
			require.NoError(t, err, "Impossible the expected wantBody file %s", tt.want.bodyFile)
			assert.Equal(t, tt.want.httpCode, rec.Code, "Invalid HTTP Code")
			assert.JSONEq(t, string(wantBody), rec.Body.String(), "Invalid response wantBody")
		})
	}
}

func Test_Evaluate_FlagEvaluationMetric(t *testing.T) {
	t.Parallel()

	const (
		metricName   = "semaphore_flag_evaluations_total"
		metricHeader = "# HELP " + metricName + " Counter events for number of flag evaluation.\n" +
			"# TYPE " + metricName + " counter\n"
	)

	tests := []struct {
		name     string
		bodyFile string
		want     string
		flagKeys []string
	}{
		{
			name:     "should use the flag key as label if the flag exists",
			flagKeys: []string{numberFlag, numberFlag, targetingKeyRule},
			bodyFile: testdataDir + ofrepValidRequestJSON,
			want: metricHeader +
				metricName + "{flag_name=\"number-flag\"} 2\n" +
				metricName + "{flag_name=\"targeting-key-rule\"} 1\n",
		},
		{
			name:     "should use the same label for all the flags that do not exist",
			flagKeys: []string{"unknown-flag-1", "unknown-flag-2", "unknown-flag-3"},
			bodyFile: testdataDir + ofrepValidRequestJSON,
			want:     metricHeader + metricName + "{flag_name=\"FLAG_NOT_FOUND\"} 3\n",
		},
		{
			name:     "should not count a request that is not evaluated",
			flagKeys: []string{numberFlag, "unknown-flag"},
			bodyFile: testdataDir + ofrepInvalidContextJSON,
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := mock.FileConfig(configFlagsLocation)
			flagsetManager := mock.NewDefault(t, &cfg)

			defer flagsetManager.Close()

			metrics, err := metric.NewMetrics()
			require.NoError(t, err)

			ctrl := ofrep.NewOFREPEvaluate(flagsetManager, metrics, ofrep.EventStream{})
			mux := http.NewServeMux()
			mux.Handle(postOfrepVOneEvaluate, helper.Handle(ctrl.Evaluate, false))

			bodyReqContent, err := os.ReadFile(tt.bodyFile)
			require.NoError(t, err, "request body file missing %s", tt.bodyFile)

			for _, flagKey := range tt.flagKeys {
				req := httptest.NewRequestWithContext(t.Context(),
					http.MethodPost, ofrepEvaluatePrefix+flagKey, strings.NewReader(string(bodyReqContent)))
				req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
				mux.ServeHTTP(httptest.NewRecorder(), req)
			}

			assert.NoError(t, testutil.GatherAndCompare(metrics.Registry, strings.NewReader(tt.want), metricName))
		})
	}
}

func Test_Evaluate_CacheableMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		opts          []ofrep.Option
		wantSemaphore bool
	}{
		{name: "both keys by default", wantSemaphore: true},
		{
			name:          "legacy only: option off",
			opts:          []ofrep.Option{ofrep.WithLegacyMetadataOnly(false)},
			wantSemaphore: true,
		},
		{name: "legacy only: option on", opts: []ofrep.Option{ofrep.WithLegacyMetadataOnly(true)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := mock.FileConfig(configFlagsLocation)
			flagsetManager := mock.NewDefault(t, &cfg)

			defer flagsetManager.Close()

			ctrl := ofrep.NewOFREPEvaluate(flagsetManager, &metric.Metrics{}, ofrep.EventStream{}, tt.opts...)
			mux := http.NewServeMux()
			mux.Handle(postOfrepVOneEvaluate, helper.Handle(ctrl.Evaluate, false))

			body, err := os.ReadFile(testdataDir + ofrepValidRequestJSON)
			require.NoError(t, err)

			req := httptest.NewRequestWithContext(t.Context(),
				http.MethodPost,
				"/ofrep/v1/evaluate/flags/number-flag",
				strings.NewReader(string(body)),
			)
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)

			var resp model.OFREPEvaluateSuccessResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, true, resp.Metadata[ofrep.LegacyMetadataCacheable], "the legacy key is always sent")
			assert.Equal(t, true, resp.Metadata["gofeatureflag_cacheable"])

			if tt.wantSemaphore {
				assert.Equal(t, true, resp.Metadata[ofrep.MetadataCacheable])
				assert.Equal(t, true, resp.Metadata["semaphore_cacheable"])
			} else {
				assert.NotContains(t, resp.Metadata, ofrep.MetadataCacheable)
			}
		})
	}
}

func mustEventStream(t *testing.T, baseURL string, delay int) ofrep.EventStream {
	t.Helper()

	stream, err := ofrep.NewEventStream(baseURL, delay)
	require.NoError(t, err)

	return stream
}
