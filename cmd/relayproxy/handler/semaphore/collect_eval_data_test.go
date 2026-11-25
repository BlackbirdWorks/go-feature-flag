package controller_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/blackbirdworks/huma-middleware/middleware/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testutils/slogutil"
)

const (
	collectEvalDataValid = "/collect_eval_data/valid_response.json"
	file2                = "file"
	invalidExportedData  = "Invalid exported data"
	test2                = "test"
)

// requestContext returns the context of a request that went through the request logger, as the relay proxy installs
// it: the logger of the context writes to logger and carries the request ID.
func requestContext(t *testing.T, logger *slog.Logger, requestID string) context.Context {
	t.Helper()

	var served *http.Request

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil)
	req.Header.Set("X-Request-ID", requestID)
	logging.Handler(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { served = r }),
		logging.WithLogger(logger)).ServeHTTP(httptest.NewRecorder(), req)

	return served.Context()
}

type collectEvalDataWant struct {
	bodyFile          string
	errorMsg          string
	collectedDataFile string
	httpCode          int
	errorCode         int
	warnings          int
	handlerErr        bool
}

type collectEvalDataArgs struct {
	bodyFile string
}

type collectEvalDataCase struct {
	name   string
	args   collectEvalDataArgs
	apiKey string
	want   collectEvalDataWant
	config config.Config
}

func runCollectEvalDataTests(t *testing.T, tests []collectEvalDataCase) {
	t.Helper()

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exporterFile, err := os.CreateTemp(t.TempDir(), "exporter.json")
			require.NoError(t, err)

			defer os.Remove(exporterFile.Name())

			setCollectEvalDataFileExporter(&tt.config, exporterFile.Name())

			flagsetManager, err := service.NewFlagsetManager(
				&tt.config,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err)

			ctrl := controller.NewCollectEvalData(flagsetManager, metric.Metrics{})

			// read wantBody request file
			var bodyReq []byte

			if tt.args.bodyFile != "" {
				var readFileErr error

				bodyReq, readFileErr = os.ReadFile(tt.args.bodyFile)
				require.NoError(t, readFileErr, requestWantBodyFileMissing, tt.args.bodyFile)
			}

			logger, logs := slogutil.NewObserver(slog.LevelDebug)
			out, handlerErr := ctrl.Handle(requestContext(t, logger, "req-1"), &controller.CollectEvalDataInput{
				Request: controller.NewRequest(tt.apiKey, applicationJSON),
				RawBody: bodyReq,
			})

			// the lines of the handler go through the logger of the request, with its request ID.
			require.Len(t, logs.All(), tt.want.warnings)

			for _, entry := range logs.All() {
				assert.Equal(t, slog.LevelWarn, entry.Level)
				assert.Equal(t, "req-1", entry.ContextMap()["request_id"])
			}

			if tt.want.handlerErr {
				assertCollectEvalDataHandlerError(t, handlerErr, tt.want.errorCode, tt.want.errorMsg)

				return
			}

			flagsetManager.Close()

			wantBody, err := os.ReadFile(tt.want.bodyFile)
			require.NoError(t, err)
			wantCollectData, err := os.ReadFile(tt.want.collectedDataFile)
			require.NoError(t, err)
			exportedData, err := os.ReadFile(exporterFile.Name())
			require.NoError(t, err)

			// replace the timestamps in the response
			regex := regexp.MustCompile(d10)
			replacedStr := regex.ReplaceAllString(jsonOf(t, out.Body), fixture1652273630)

			// validate the result
			require.NoError(t, err, impossibleTheExpectedWantBody, tt.want.bodyFile)
			assert.Equal(t, http.StatusOK, tt.want.httpCode, invalidHTTPCode)
			assert.JSONEq(t, string(wantBody), replacedStr, invalidResponseWantBody)
			assert.JSONEq(t, string(wantCollectData), string(exportedData), invalidExportedData)
		})
	}
}

func Test_collect_eval_data_Handler(t *testing.T) {
	t.Parallel()

	runCollectEvalDataTests(t, []collectEvalDataCase{
		{
			name: "valid usecase",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: file2,
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: collectEvalDataArgs{
				bodyFile: testdataDir + "/collect_eval_data/valid_request.json",
			},
			want: collectEvalDataWant{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + collectEvalDataValid,
				collectedDataFile: testdataDir + "/collect_eval_data/valid_collected_data.json",
			},
		},
		{
			name:   "valid usecase flagset",
			apiKey: test2,
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{test2},
						PollingInterval: 10,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: file2,
								Path: configFlagsLocation,
							},
						},
					},
				},
			},
			args: collectEvalDataArgs{
				bodyFile: testdataDir + "/collect_eval_data/valid_request.json",
			},
			want: collectEvalDataWant{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + collectEvalDataValid,
				collectedDataFile: testdataDir + "/collect_eval_data/valid_collected_data.json",
			},
		},
		{
			name: "valid with source field",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: file2,
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: collectEvalDataArgs{
				bodyFile: testdataDir + "/collect_eval_data/request_with_source_field.json",
			},
			want: collectEvalDataWant{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + collectEvalDataValid,
				collectedDataFile: testdataDir + "/collect_eval_data/collected_data_with_source_field.json",
			},
		},
		{
			name:   "valid with source field flagset",
			apiKey: test2,
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{test2},
						PollingInterval: 10,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: file2,
								Path: configFlagsLocation,
							},
						},
					},
				},
			},
			args: collectEvalDataArgs{
				bodyFile: testdataDir + "/collect_eval_data/request_with_source_field.json",
			},
			want: collectEvalDataWant{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + collectEvalDataValid,
				collectedDataFile: testdataDir + "/collect_eval_data/collected_data_with_source_field.json",
			},
		},
	})
}

func Test_collect_eval_data_Handler_ErrorsAndMetadata(t *testing.T) {
	t.Parallel()

	runCollectEvalDataTests(t, []collectEvalDataCase{
		{
			name: "invalid json",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: file2,
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: collectEvalDataArgs{
				bodyFile: testdataDir + "/collect_eval_data/invalid_request.json",
			},
			want: collectEvalDataWant{
				handlerErr: true,
				httpCode:   http.StatusBadRequest,
				errorMsg: "collectEvalData: invalid input data code=400, message=Bad Request, " +
					"err=invalid character '}' after array element",
				errorCode: http.StatusBadRequest,
			},
		},
		{
			name: "invalid data field",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: file2,
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: collectEvalDataArgs{
				bodyFile: testdataDir + "/collect_eval_data/invalid_request_data_null.json",
			},
			want: collectEvalDataWant{
				handlerErr: true,
				httpCode:   http.StatusBadRequest,
				errorMsg:   "collectEvalData: invalid input data",
				errorCode:  http.StatusBadRequest,
			},
		},
		{
			name: "be sure that the creation date is a unix timestamp",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: file2,
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: collectEvalDataArgs{
				testdataDir + "/collect_eval_data/valid_request_with_timestamp_ms.json",
			},
			want: collectEvalDataWant{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + collectEvalDataValid,
				collectedDataFile: testdataDir + "/collect_eval_data/valid_collected_data_with_timestamp_ms.json",
				warnings:          1,
			},
		},
		{
			name: "should have the metadata in the exporter",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: file2,
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: collectEvalDataArgs{
				testdataDir + "/collect_eval_data/valid_request_metadata.json",
			},
			want: collectEvalDataWant{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + collectEvalDataValid,
				collectedDataFile: testdataDir + "/collect_eval_data/valid_collected_data_metadata.json",
			},
		},
	})
}

// setCollectEvalDataFileExporter configures cfg to export collected data to the
// given file, handling both the flag-set and the legacy single-config layouts.
func setCollectEvalDataFileExporter(cfg *config.Config, filename string) {
	exporter := config.ExporterConf{
		Kind:     file2,
		Filename: filename,
	}
	if len(cfg.FlagSets) == 0 {
		cfg.Exporters = &[]config.ExporterConf{exporter}

		return
	}

	out := make([]config.FlagSet, len(cfg.FlagSets))
	for index, flagSet := range cfg.FlagSets {
		flagSet.Exporters = &[]config.ExporterConf{exporter}
		out[index] = flagSet
	}

	cfg.FlagSets = out
}

// assertCollectEvalDataHandlerError asserts that the handler returned the
// expected error, supporting both helper.HTTPError and plain error values.
func assertCollectEvalDataHandlerError(t *testing.T, handlerErr error, errorCode int, errorMsg string) {
	t.Helper()
	require.Error(t, handlerErr, handlerShouldReturnAn)

	httpErr := &helper.HTTPError{}

	ok := errors.As(handlerErr, &httpErr)
	if !ok {
		assert.Equal(t, errorMsg, handlerErr.Error())

		return
	}

	assert.Equal(t, errorCode, httpErr.Code)
	assert.Equal(t, errorMsg, httpErr.Message)
}

func TestCollectEvalData_Handler_cancellation(t *testing.T) {
	t.Parallel()

	flagsetManager, err := service.NewFlagsetManager(&config.Config{
		PollingInterval: 10,
		Retrievers:      &[]retrieverconf.RetrieverConf{{Kind: file2, Path: configFlagsLocation}},
	}, slog.New(slog.DiscardHandler), []notifier.Notifier{}, nil, nil)
	require.NoError(t, err)

	t.Cleanup(func() { flagsetManager.Close() })

	ctrl := controller.NewCollectEvalData(flagsetManager, metric.Metrics{})

	// large payload with 20,000 events to ensure processing takes time
	event := `{"kind":"feature","contextKind":"user","userKey":"u","creationDate":1680246000,"key":"f","variation":"` +
		`v","value":"true","default":false,"version":"1","source":"PROVIDER_CACHE"}`
	body := `{"events":[` + strings.Repeat(event+",", 19999) + event + `]}`

	ctx, cancel := context.WithCancel(t.Context())
	input := &controller.CollectEvalDataInput{
		Request: controller.NewRequest("", applicationJSON),
		RawBody: []byte(body),
	}

	// run handler in background, cancel after 10ms
	done := make(chan error, 1)

	go func() {
		_, handleErr := ctrl.Handle(ctx, input)
		done <- handleErr
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	err = <-done

	require.ErrorIs(t, err, context.Canceled)
	assert.Contains(t, err.Error(), "context cancelled after processing")
	// count will be less than the total if cancellation worked
	assert.NotContains(t, err.Error(), "20000/20000")
}

const (
	wantEvalEvent = "{\"kind\":\"feature\",\"contextKind\":\"user\",\"userKey\":\"94a25909-20d8-40cc-8500-fee99b" +
		"569345\",\"creationDate\":1680246000,\"key\":\"my-feature-flag\",\"variation\":\"admin-variation\",\"value\":" +
		"\"string\",\"default\":false,\"version\":\"v1.0.0\",\"source\":\"PROVIDER_CACHE\",\"metadata\":{\"environment" +
		"\":\"production\",\"sdkVersion\":\"v1.0.0\",\"source\":\"my-source\",\"timestamp\":1680246000}}\n"
	wantTrackingEvent = "{\"kind\":\"tracking\",\"contextKind\":\"user\",\"userKey\":\"94a25909-20d8-40cc-8500-fee99" +
		"b569345\",\"creationDate\":1680246020,\"key\":\"my-feature-flag\",\"evaluationContext\":{\"admin\":true,\"name" +
		"\":\"john doe\",\"targetingKey\":\"94a25909-20d8-40cc-8500-fee99b569345\"},\"trackingEventDetails\":{\"value\"" +
		":\"string\",\"version\":\"v1.0.0\"}}\n"
)

func Test_collect_tracking_and_evaluation_events(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		apiKey       string
		wantEval     string
		wantTracking string
		config       config.Config
	}{
		{
			name: defaultMode,
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: file2,
							Path: configFlagsLocation,
						},
					},
				},
			},
			wantEval:     wantEvalEvent,
			wantTracking: wantTrackingEvent,
		},
		{
			name:   flagsetMode,
			apiKey: test2,
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{test2},
						PollingInterval: 10,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: file2,
								Path: configFlagsLocation,
							},
						},
					},
				},
			},
			wantEval:     wantEvalEvent,
			wantTracking: wantTrackingEvent,
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evalExporter, err := os.CreateTemp(t.TempDir(), "evalExport.json")
			require.NoError(t, err)
			trackingExporter, err := os.CreateTemp(t.TempDir(), "trackExport.json")
			require.NoError(t, err)

			defer func() {
				_ = os.Remove(evalExporter.Name())
				_ = os.Remove(trackingExporter.Name())
			}()

			if len(tt.config.FlagSets) > 0 {
				out := make([]config.FlagSet, len(tt.config.FlagSets))

				for index, flagSet := range tt.config.FlagSets {
					flagSet.Exporters = &[]config.ExporterConf{
						{
							Kind:             config.FileExporter,
							Filename:         evalExporter.Name(),
							MaxEventInMemory: 10000,
							FlushInterval:    int64(10 * time.Second),
						},
						{
							Kind:              config.FileExporter,
							Filename:          trackingExporter.Name(),
							ExporterEventType: ffclient.TrackingEventExporter,
							MaxEventInMemory:  10000,
							FlushInterval:     int64(10 * time.Second),
						},
					}
					out[index] = flagSet
				}

				tt.config.FlagSets = out
			} else {
				tt.config.Exporters = &[]config.ExporterConf{
					{
						Kind:             file2,
						Filename:         evalExporter.Name(),
						MaxEventInMemory: 10000,
						FlushInterval:    int64(10 * time.Second),
					},
					{
						Kind:              file2,
						Filename:          trackingExporter.Name(),
						ExporterEventType: ffclient.TrackingEventExporter,
						MaxEventInMemory:  10000,
						FlushInterval:     int64(10 * time.Second),
					},
				}
			}

			flagsetManager, err := service.NewFlagsetManager(
				&tt.config,
				slog.New(slog.DiscardHandler),
				[]notifier.Notifier{},
				nil,
				nil,
			)
			require.NoError(t, err)

			ctrl := controller.NewCollectEvalData(flagsetManager, metric.Metrics{})
			bodyReq, err := os.ReadFile(
				testdataDir + "/collect_eval_data/valid_request_mix_tracking_evaluation.json")
			require.NoError(t, err)
			_, handlerErr := ctrl.Handle(t.Context(), &controller.CollectEvalDataInput{
				Request: controller.NewRequest(tt.apiKey, applicationJSON),
				RawBody: bodyReq,
			})
			require.NoError(t, handlerErr)
			flagsetManager.Close()

			evalEvents, err := os.ReadFile(evalExporter.Name())
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantEval, string(evalEvents), invalidExportedData)

			trackingEvents, err := os.ReadFile(trackingExporter.Name())
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantTracking, string(trackingEvents), invalidExportedData)
		})
	}
}
