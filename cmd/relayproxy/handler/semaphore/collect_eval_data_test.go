package controller_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

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
)

func Test_collect_eval_data_Handler(t *testing.T) {
	t.Parallel()

	type want struct {
		bodyFile          string
		errorMsg          string
		collectedDataFile string
		httpCode          int
		errorCode         int
		handlerErr        bool
	}
	type args struct {
		bodyFile string
	}
	tests := []struct {
		name   string
		args   args
		apiKey string
		want   want
		config config.Config
	}{
		{
			name: "valid usecase",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: "file",
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: args{
				bodyFile: testdataDir + "/collect_eval_data/valid_request.json",
			},
			want: want{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + "/collect_eval_data/valid_response.json",
				collectedDataFile: testdataDir + "/collect_eval_data/valid_collected_data.json",
			},
		},
		{
			name:   "valid usecase flagset",
			apiKey: "test",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{"test"},
						PollingInterval: 10,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: "file",
								Path: configFlagsLocation,
							},
						},
					},
				},
			},
			args: args{
				bodyFile: testdataDir + "/collect_eval_data/valid_request.json",
			},
			want: want{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + "/collect_eval_data/valid_response.json",
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
							Kind: "file",
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: args{
				bodyFile: testdataDir + "/collect_eval_data/request_with_source_field.json",
			},
			want: want{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + "/collect_eval_data/valid_response.json",
				collectedDataFile: testdataDir + "/collect_eval_data/collected_data_with_source_field.json",
			},
		},
		{
			name:   "valid with source field flagset",
			apiKey: "test",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{"test"},
						PollingInterval: 10,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: "file",
								Path: configFlagsLocation,
							},
						},
					},
				},
			},
			args: args{
				bodyFile: testdataDir + "/collect_eval_data/request_with_source_field.json",
			},
			want: want{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + "/collect_eval_data/valid_response.json",
				collectedDataFile: testdataDir + "/collect_eval_data/collected_data_with_source_field.json",
			},
		},
		{
			name: "invalid json",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: "file",
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: args{
				bodyFile: testdataDir + "/collect_eval_data/invalid_request.json",
			},
			want: want{
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
							Kind: "file",
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: args{
				bodyFile: testdataDir + "/collect_eval_data/invalid_request_data_null.json",
			},
			want: want{
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
							Kind: "file",
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: args{
				testdataDir + "/collect_eval_data/valid_request_with_timestamp_ms.json",
			},
			want: want{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + "/collect_eval_data/valid_response.json",
				collectedDataFile: testdataDir + "/collect_eval_data/valid_collected_data_with_timestamp_ms.json",
			},
		},
		{
			name: "should have the metadata in the exporter",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: "file",
							Path: configFlagsLocation,
						},
					},
				},
			},
			args: args{
				testdataDir + "/collect_eval_data/valid_request_metadata.json",
			},
			want: want{
				httpCode:          http.StatusOK,
				bodyFile:          testdataDir + "/collect_eval_data/valid_response_metadata.json",
				collectedDataFile: testdataDir + "/collect_eval_data/valid_collected_data_metadata.json",
			},
		},
	}
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

			logger := slog.New(slog.DiscardHandler)
			ctrl := controller.NewCollectEvalData(flagsetManager, metric.Metrics{}, logger)

			// read wantBody request file
			var bodyReq []byte
			if tt.args.bodyFile != "" {
				var readFileErr error
				bodyReq, readFileErr = os.ReadFile(tt.args.bodyFile)
				require.NoError(t, readFileErr, "request wantBody file missing %s", tt.args.bodyFile)
			}

			out, handlerErr := ctrl.Handle(t.Context(), &controller.CollectEvalDataInput{
				Request: controller.NewRequest(tt.apiKey, "application/json"),
				RawBody: bodyReq,
			})

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
			regex := regexp.MustCompile(`\d{10}`)
			replacedStr := regex.ReplaceAllString(jsonOf(t, out.Body), "1652273630")

			// validate the result
			require.NoError(t, err, "Impossible the expected wantBody file %s", tt.want.bodyFile)
			assert.Equal(t, http.StatusOK, tt.want.httpCode, "Invalid HTTP Code")
			assert.JSONEq(t, string(wantBody), replacedStr, "Invalid response wantBody")
			assert.JSONEq(t, string(wantCollectData), string(exportedData), "Invalid exported data")
		})
	}
}

// setCollectEvalDataFileExporter configures cfg to export collected data to the
// given file, handling both the flag-set and the legacy single-config layouts.
func setCollectEvalDataFileExporter(cfg *config.Config, filename string) {
	exporter := config.ExporterConf{
		Kind:     "file",
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
	require.Error(t, handlerErr, "handler should return an error")
	he := &helper.HTTPError{}
	ok := errors.As(handlerErr, &he)
	if !ok {
		assert.Equal(t, errorMsg, handlerErr.Error())

		return
	}
	assert.Equal(t, errorCode, he.Code)
	assert.Equal(t, errorMsg, he.Message)
}

func TestCollectEvalData_Handler_cancellation(t *testing.T) {
	t.Parallel()

	flagsetManager, err := service.NewFlagsetManager(&config.Config{
		PollingInterval: 10,
		Retrievers:      &[]retrieverconf.RetrieverConf{{Kind: "file", Path: configFlagsLocation}},
	}, slog.New(slog.DiscardHandler), []notifier.Notifier{}, nil, nil)
	require.NoError(t, err)

	t.Cleanup(func() { flagsetManager.Close() })

	ctrl := controller.NewCollectEvalData(flagsetManager, metric.Metrics{}, slog.New(slog.DiscardHandler))

	// large payload with 20,000 events to ensure processing takes time
	event := `{"kind":"feature","contextKind":"user","userKey":"u","creationDate":1680246000,"key":"f","variation":"` +
		`v","value":"true","default":false,"version":"1","source":"PROVIDER_CACHE"}`
	body := `{"events":[` + strings.Repeat(event+",", 19999) + event + `]}`

	ctx, cancel := context.WithCancel(t.Context())
	in := &controller.CollectEvalDataInput{
		Request: controller.NewRequest("", "application/json"),
		RawBody: []byte(body),
	}

	// run handler in background, cancel after 10ms
	done := make(chan error, 1)
	go func() {
		_, handleErr := ctrl.Handle(ctx, in)
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
			name: "default mode",
			config: config.Config{
				CommonFlagSet: config.CommonFlagSet{
					PollingInterval: 10,
					Retrievers: &[]retrieverconf.RetrieverConf{
						{
							Kind: "file",
							Path: configFlagsLocation,
						},
					},
				},
			},
			wantEval:     wantEvalEvent,
			wantTracking: wantTrackingEvent,
		},
		{
			name:   "flagset mode",
			apiKey: "test",
			config: config.Config{
				FlagSets: []config.FlagSet{
					{
						APIKeys:         []string{"test"},
						PollingInterval: 10,
						Retrievers: &[]retrieverconf.RetrieverConf{
							{
								Kind: "file",
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
						Kind:             "file",
						Filename:         evalExporter.Name(),
						MaxEventInMemory: 10000,
						FlushInterval:    int64(10 * time.Second),
					},
					{
						Kind:              "file",
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
			ctrl := controller.NewCollectEvalData(flagsetManager, metric.Metrics{}, slog.New(slog.DiscardHandler))
			bodyReq, err := os.ReadFile(
				testdataDir + "/collect_eval_data/valid_request_mix_tracking_evaluation.json")
			require.NoError(t, err)
			_, handlerErr := ctrl.Handle(t.Context(), &controller.CollectEvalDataInput{
				Request: controller.NewRequest(tt.apiKey, "application/json"),
				RawBody: bodyReq,
			})
			require.NoError(t, handlerErr)
			flagsetManager.Close()
			evalEvents, err := os.ReadFile(evalExporter.Name())
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantEval, string(evalEvents), "Invalid exported data")
			trackingEvents, err := os.ReadFile(trackingExporter.Name())
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantTracking, string(trackingEvents), "Invalid exported data")
		})
	}
}
