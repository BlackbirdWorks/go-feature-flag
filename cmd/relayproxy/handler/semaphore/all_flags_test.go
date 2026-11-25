package controller_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

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
	badRequest                    = "Bad Request"
	contentType2                  = "Content-Type"
	d10                           = `\d{10}`
	fixedTimestamp                = "1652273630"
	handlerShouldReturnAn         = "handler should return an error"
	impossibleTheExpectedWantBody = "Impossible the expected wantBody file %s"
	invalidHTTPCode               = "Invalid HTTP Code"
	invalidJSONFormat             = "Invalid json format"
	invalidResponseWantBody       = "Invalid response wantBody"
	requestWantBodyFileMissing    = "request wantBody file missing %s"
	validFlag                     = "valid flag"
)

const testdataDir = "../../testdata/semaphore"

func Test_all_flag_Handler_DefaultMode(t *testing.T) {
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
			name: validFlag,
			args: args{
				bodyFile:            testdataDir + "/all_flags/valid_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/all_flags/valid_response.json",
			},
		},
		{
			name: invalidJSONFormat,
			args: args{
				bodyFile:            testdataDir + "/all_flags/invalid_json_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				handlerErr: true,
				errorMsg:   badRequest,
				errorCode:  http.StatusBadRequest,
			},
		},
		{
			name: "No user key in payload - should try to evaluate flags individually",
			// the API should return targeting key missing for all flags that require bucketing
			// and it should also perform all the static evaluations for non-bucketing flags
			args: args{
				bodyFile:            testdataDir + "/all_flags/no_user_key_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				handlerErr: false,
				httpCode:   http.StatusOK,
				bodyFile:   testdataDir + "/all_flags/no_user_key_response_updated.json",
			},
		},
		{
			name: "specify flags in evaluation context",
			args: args{
				bodyFile:            testdataDir + "/all_flags/valid_request_specify_flags.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/all_flags/valid_response_specify_flags.json",
			},
		},
		{
			name: "legacy user field is rejected and points to evaluationContext",
			args: args{
				bodyFile:            testdataDir + "/all_flags/legacy_user_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				handlerErr: true,
				errorMsg: "assertRequest: the 'user' field is no longer supported, " +
					"send an 'evaluationContext' instead",
				errorCode: http.StatusBadRequest,
			},
		},
		{
			name: "evaluation context without custom field should not crash",
			args: args{
				bodyFile:            testdataDir + "/all_flags/invalid_evaluation_context_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/all_flags/invalid_evaluation_context_response.json",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := config.Config{
				Retriever: &retrieverconf.RetrieverConf{
					Kind: retrieverconf.FileRetriever,
					Path: tt.args.configFlagsLocation,
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

			ctrl := controller.NewAllFlags(flagsetManager, metric.Metrics{})
			rec := httptest.NewRecorder()

			// read wantBody request file
			var bodyReq io.Reader

			if tt.args.bodyFile != "" {
				bodyReqContent, readFileErr := os.ReadFile(tt.args.bodyFile)
				require.NoError(t, readFileErr, requestWantBodyFileMissing, tt.args.bodyFile)

				bodyReq = strings.NewReader(string(bodyReqContent))
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/allflags", bodyReq)
			req.Header.Set(contentType2, applicationJSON)
			handlerErr := ctrl.Handler(rec, req)

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

			// replace the timestamps in the response
			regex := regexp.MustCompile(d10)
			replacedStr := regex.ReplaceAllString(rec.Body.String(), fixedTimestamp)

			// validate the result
			require.NoError(t, err, impossibleTheExpectedWantBody, tt.want.bodyFile)
			assert.Equal(t, tt.want.httpCode, rec.Code, invalidHTTPCode)
			assert.JSONEq(t, string(wantBody), replacedStr, invalidResponseWantBody)
		})
	}
}

func Test_all_flag_Handler_FlagsetMode(t *testing.T) {
	t.Parallel()

	const configFlagsLocation = testdataDir + configFlagsYaml

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
			name: validFlag,
			args: args{
				bodyFile:            testdataDir + "/all_flags/valid_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/all_flags/valid_response.json",
			},
		},
		{
			name: invalidJSONFormat,
			args: args{
				bodyFile:            testdataDir + "/all_flags/invalid_json_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				handlerErr: true,
				errorMsg:   badRequest,
				errorCode:  http.StatusBadRequest,
			},
		},
		{
			name: "No user key in payload - should now pass and evaluate flags individually",
			// the API should return targeting key missing for all flags that require bucketing
			// and it should also perform all the static evaluations for non-bucketing flags
			args: args{
				bodyFile:            testdataDir + "/all_flags/no_user_key_request.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				handlerErr: false, // No longer fails at API validation level
				httpCode:   http.StatusOK,
				bodyFile:   testdataDir + "/all_flags/no_user_key_response_updated.json",
			},
		},
		{
			name: "specify flags in evaluation context",
			args: args{
				bodyFile:            testdataDir + "/all_flags/valid_request_specify_flags.json",
				configFlagsLocation: configFlagsLocation,
			},
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/all_flags/valid_response_specify_flags.json",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conf := config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys: []string{"test-api-key"},
						Retriever: &retrieverconf.RetrieverConf{
							Kind: retrieverconf.FileRetriever,
							Path: tt.args.configFlagsLocation,
						},
						Exporter: &config.ExporterConf{
							Kind: config.LogExporter,
						}},
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

			ctrl := controller.NewAllFlags(flagsetManager, metric.Metrics{})
			rec := httptest.NewRecorder()

			// read wantBody request file
			var bodyReq io.Reader

			if tt.args.bodyFile != "" {
				bodyReqContent, readFileErr := os.ReadFile(tt.args.bodyFile)
				require.NoError(t, readFileErr, requestWantBodyFileMissing, tt.args.bodyFile)

				bodyReq = strings.NewReader(string(bodyReqContent))
			}

			req := httptest.NewRequest(http.MethodPost, "/v1/allflags", bodyReq)
			req.Header.Set(contentType2, applicationJSON)
			req.Header.Set("Authorization", "Bearer test-api-key")
			handlerErr := ctrl.Handler(rec, req)

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

			// replace the timestamps in the response
			regex := regexp.MustCompile(d10)
			replacedStr := regex.ReplaceAllString(rec.Body.String(), fixedTimestamp)

			// validate the result
			require.NoError(t, err, impossibleTheExpectedWantBody, tt.want.bodyFile)
			assert.Equal(t, tt.want.httpCode, rec.Code, invalidHTTPCode)
			assert.JSONEq(t, string(wantBody), replacedStr, invalidResponseWantBody)
		})
	}
}
