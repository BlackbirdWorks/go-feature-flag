package controller_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	flagEvalValidRequest = "/flag_eval/valid_request.json"
	flagOnlyForAdmin     = "flag-only-for-admin"
	testFlagRuleApply    = "test-flag-rule-apply-false"
)

const configFlagsLocation = testdataDir + configFlagsYaml

func Test_flag_eval_Handler(t *testing.T) {
	t.Parallel()

	type want struct {
		bodyFile   string
		errorMsg   string
		httpCode   int
		errorCode  int
		handlerErr bool
	}

	type args struct {
		flagKey  string
		bodyFile string
	}

	tests := []struct {
		name string
		args args
		want want
	}{
		{
			name: validFlag,
			args: args{
				flagKey:  flagOnlyForAdmin,
				bodyFile: testdataDir + flagEvalValidRequest,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/flag_eval/valid_response.json",
			},
		},
		{
			name: "Get default value if flag disable",
			args: args{
				flagKey:  "disable-flag",
				bodyFile: testdataDir + "/flag_eval/disable_flag_request.json",
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/flag_eval/disable_flag_response.json",
			},
		},
		{
			name: "Get default value with key not exist",
			args: args{
				flagKey:  "random-key-does-not-exist",
				bodyFile: testdataDir + "/flag_eval/flag_not_exist_request.json",
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/flag_eval/flag_not_exist_response.json",
			},
		},
		{
			name: "Get default value, rule not apply",
			args: args{
				flagKey:  "test-flag-rule-not-apply",
				bodyFile: testdataDir + "/flag_eval/rule_not_apply_request.json",
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/flag_eval/rule_not_apply_response.json",
			},
		},
		{
			name: "Get true value, rule apply",
			args: args{
				flagKey:  "test-flag-rule-apply",
				bodyFile: testdataDir + "/flag_eval/rule_apply_request.json",
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/flag_eval/rule_apply_response.json",
			},
		},
		{
			name: "Get false value, rule apply",
			args: args{
				flagKey:  testFlagRuleApply,
				bodyFile: testdataDir + "/flag_eval/rule_apply_false_request.json",
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/flag_eval/rule_apply_false_response.json",
			},
		},
		{
			name: invalidJSONFormat,
			args: args{
				flagKey:  testFlagRuleApply,
				bodyFile: testdataDir + "/flag_eval/invalid_json_request.json",
			},
			want: want{
				handlerErr: true,
				errorMsg:   badRequest,
				errorCode:  http.StatusBadRequest,
			},
		},
		{
			name: "No user key in payload",
			args: args{
				flagKey:  testFlagRuleApply,
				bodyFile: testdataDir + "/flag_eval/no_user_key_request.json",
			},
			want: want{
				handlerErr: false,
				bodyFile:   testdataDir + "/flag_eval/no_user_key_response.json",
				httpCode:   http.StatusOK,
			},
		},
		{
			name: "legacy user field is rejected and points to evaluationContext",
			args: args{
				flagKey:  testFlagRuleApply,
				bodyFile: testdataDir + "/flag_eval/legacy_user_request.json",
			},
			want: want{
				handlerErr: true,
				errorMsg: "assertRequest: the 'user' field is no longer supported, " +
					"send an 'evaluationContext' instead",
				errorCode: http.StatusBadRequest,
			},
		},
		{
			name: "no flag key in URL",
			args: args{
				flagKey:  "",
				bodyFile: testdataDir + flagEvalValidRequest,
			},
			want: want{
				handlerErr: true,
				errorMsg:   "impossible to find the flag key in the URL",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create config for default mode
			conf := config.Config{
				Retriever: &retrieverconf.RetrieverConf{
					Kind: retrieverconf.FileRetriever,
					Path: configFlagsLocation,
				},
				Exporter: &config.ExporterConf{
					Kind: config.LogExporter,
				},
			}

			flagsetManager, err := service.NewFlagsetManager(
				&conf,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err, impossibleToCreateFlagset)

			flagEval := controller.NewFlagEval(flagsetManager, metric.Metrics{})
			rec := httptest.NewRecorder()

			// read wantBody request file
			var bodyReq io.Reader

			if tt.args.bodyFile != "" {
				bodyReqContent, readFileErr := os.ReadFile(tt.args.bodyFile)
				require.NoError(t, readFileErr, requestWantBodyFileMissing, tt.args.bodyFile)

				bodyReq = strings.NewReader(string(bodyReqContent))
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/feature/"+tt.args.flagKey+"/eval", bodyReq)
			req.Header.Set(contentType2, applicationJSON)
			req.SetPathValue("flagKey", tt.args.flagKey)
			handlerErr := flagEval.Handler(rec, req)

			if tt.want.handlerErr {
				require.Error(t, handlerErr, handlerShouldReturnAn)

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
			require.NoError(t, err, impossibleTheExpectedWantBody, tt.want.bodyFile)
			assert.Equal(t, tt.want.httpCode, rec.Code, invalidHTTPCode)
			assert.JSONEq(t, string(wantBody), rec.Body.String(), invalidResponseWantBody)
		})
	}
}

func Test_flag_eval_Handler_FlagEvaluationMetric(t *testing.T) {
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
			flagKeys: []string{flagOnlyForAdmin, flagOnlyForAdmin, "test-flag-rule-apply"},
			bodyFile: testdataDir + flagEvalValidRequest,
			want: metricHeader +
				metricName + "{flag_name=\"flag-only-for-admin\"} 2\n" +
				metricName + "{flag_name=\"test-flag-rule-apply\"} 1\n",
		},
		{
			name:     "should use the same label for all the flags that do not exist",
			flagKeys: []string{"unknown-flag-1", "unknown-flag-2", "unknown-flag-3"},
			bodyFile: testdataDir + "/flag_eval/flag_not_exist_request.json",
			want:     metricHeader + metricName + "{flag_name=\"FLAG_NOT_FOUND\"} 3\n",
		},
		{
			name:     "should not count a request that is not evaluated",
			flagKeys: []string{flagOnlyForAdmin, "unknown-flag"},
			bodyFile: testdataDir + "/flag_eval/invalid_json_request.json",
			want:     "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := config.Config{
				Retriever: &retrieverconf.RetrieverConf{
					Kind: retrieverconf.FileRetriever,
					Path: configFlagsLocation,
				},
			}

			flagsetManager, err := service.NewFlagsetManager(
				&conf,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err, impossibleToCreateFlagset)

			defer flagsetManager.Close()

			metrics, err := metric.NewMetrics()
			require.NoError(t, err)

			flagEval := controller.NewFlagEval(flagsetManager, metrics)

			bodyReqContent, err := os.ReadFile(tt.bodyFile)
			require.NoError(t, err, "request body file missing %s", tt.bodyFile)

			for _, flagKey := range tt.flagKeys {
				req := httptest.NewRequestWithContext(t.Context(),
					http.MethodPost, "/v1/feature/"+flagKey+"/eval", strings.NewReader(string(bodyReqContent)))
				req.Header.Set(contentType2, applicationJSON)
				req.SetPathValue("flagKey", flagKey)
				_ = flagEval.Handler(httptest.NewRecorder(), req)
			}

			assert.NoError(t, testutil.GatherAndCompare(metrics.Registry, strings.NewReader(tt.want), metricName))
		})
	}
}
