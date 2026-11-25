package fflog_test

import (
	"context"
	"log"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	debugMessage                 = "Debug message"
	errorMessage                 = "Error message"
	ffLoggerNilMessage           = "FFLogger nil"
	ffLoggerNotConfiguredMessage = "FFLogger no logger configured"
	timestampSample              = "2006/01/02 15:04:05 "
	infoMessage                  = "Info message"
	req1                         = "req-1"
	test                         = "test"
	testHappyPathLegacy          = "Test Happy Path - legacy"
	testHappyPathLegacy2         = "Test Happy Path - legacy with keys and values"
	testHappyPathSlog            = "Test Happy Path - slog"
	testHappyPathSlog2           = "Test Happy Path - slog with keys and values"
	toto                         = "toto"
	warnMessage                  = "Warn message"
)

type ctxKey string

type ctxCaptureHandler struct {
	slog.Handler

	gotVal any
	key    ctxKey
	nonNil bool
}

func (h *ctxCaptureHandler) Handle(ctx context.Context, r slog.Record) error {
	h.nonNil = ctx != nil
	if ctx != nil {
		h.gotVal = ctx.Value(h.key)
	}

	return h.Handler.Handle(ctx, r)
}

func TestFFLogger_Error(t *testing.T) {
	t.Parallel()

	type fields struct {
		msg           string
		keysAndValues []any
	}

	tests := []struct {
		logger      *fflog.FFLogger
		name        string
		expectedLog string
		fields      fields
	}{
		{
			name: testHappyPathSlog,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           errorMessage,
				keysAndValues: nil,
			},
			expectedLog: `level=ERROR msg="Error message"` + "\n",
		},
		{
			name: testHappyPathSlog2,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: errorMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: `level=ERROR msg="Error message" test=toto toto=test` + "\n",
		},
		{
			name: testHappyPathLegacy,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           errorMessage,
				keysAndValues: nil,
			},
			expectedLog: "ERROR Error message" + "\n",
		},
		{
			name: testHappyPathLegacy2,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: errorMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: "ERROR Error message test=toto toto=test" + "\n",
		},
		{
			name:   ffLoggerNilMessage,
			logger: nil,
			fields: fields{
				msg:           errorMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   ffLoggerNotConfiguredMessage,
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           errorMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := os.CreateTemp(t.TempDir(), "")
			require.NoError(t, err)

			defer func() {
				_ = os.Remove(file.Name())
			}()

			legacy := tt.logger != nil && tt.logger.LeveledLogger == nil && tt.logger.LegacyLogger != nil
			if tt.logger != nil && tt.logger.LeveledLogger != nil {
				tt.logger.LeveledLogger = slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{
					Level: slog.LevelDebug,
					ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
						if a.Key == slog.TimeKey {
							return slog.Attr{}
						}

						return a
					},
				}))
			}

			if tt.logger != nil && tt.logger.LegacyLogger != nil {
				tt.logger.LegacyLogger.SetOutput(file)
			}

			if tt.fields.keysAndValues != nil {
				tt.logger.Error(tt.fields.msg, tt.fields.keysAndValues...)
			} else {
				tt.logger.Error(tt.fields.msg)
			}

			content, err := os.ReadFile(file.Name())
			require.NoError(t, err)

			actual := string(content)

			if legacy {
				// The legacy logger prefixes the lines with the current time (yyyy/mm/dd hh:mm:ss ).
				const timestampLen = len(timestampSample)
				if len(actual) >= timestampLen {
					actual = actual[timestampLen:]
				}
			}

			assert.Equal(t, tt.expectedLog, actual)
		})
	}
}

func TestFFLogger_Warn(t *testing.T) {
	t.Parallel()

	type fields struct {
		msg           string
		keysAndValues []any
	}

	tests := []struct {
		logger      *fflog.FFLogger
		name        string
		expectedLog string
		fields      fields
	}{
		{
			name: testHappyPathSlog,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           warnMessage,
				keysAndValues: nil,
			},
			expectedLog: `level=WARN msg="Warn message"` + "\n",
		},
		{
			name: testHappyPathSlog2,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: warnMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: `level=WARN msg="Warn message" test=toto toto=test` + "\n",
		},
		{
			name: testHappyPathLegacy,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           warnMessage,
				keysAndValues: nil,
			},
			expectedLog: "WARN Warn message" + "\n",
		},
		{
			name: testHappyPathLegacy2,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: warnMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: "WARN Warn message test=toto toto=test" + "\n",
		},
		{
			name:   ffLoggerNilMessage,
			logger: nil,
			fields: fields{
				msg:           warnMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   ffLoggerNotConfiguredMessage,
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           warnMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := os.CreateTemp(t.TempDir(), "")
			require.NoError(t, err)

			defer func() {
				_ = os.Remove(file.Name())
			}()

			legacy := tt.logger != nil && tt.logger.LeveledLogger == nil && tt.logger.LegacyLogger != nil
			if tt.logger != nil && tt.logger.LeveledLogger != nil {
				tt.logger.LeveledLogger = slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{
					Level: slog.LevelDebug,
					ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
						if a.Key == slog.TimeKey {
							return slog.Attr{}
						}

						return a
					},
				}))
			}

			if tt.logger != nil && tt.logger.LegacyLogger != nil {
				tt.logger.LegacyLogger.SetOutput(file)
			}

			if tt.fields.keysAndValues != nil {
				tt.logger.Warn(tt.fields.msg, tt.fields.keysAndValues...)
			} else {
				tt.logger.Warn(tt.fields.msg)
			}

			content, err := os.ReadFile(file.Name())
			require.NoError(t, err)

			actual := string(content)

			if legacy {
				// The legacy logger prefixes the lines with the current time (yyyy/mm/dd hh:mm:ss ).
				const timestampLen = len(timestampSample)
				if len(actual) >= timestampLen {
					actual = actual[timestampLen:]
				}
			}

			assert.Equal(t, tt.expectedLog, actual)
		})
	}
}

func TestFFLogger_Info(t *testing.T) {
	t.Parallel()

	type fields struct {
		msg           string
		keysAndValues []any
	}

	tests := []struct {
		logger      *fflog.FFLogger
		name        string
		expectedLog string
		fields      fields
	}{
		{
			name: testHappyPathSlog,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           infoMessage,
				keysAndValues: nil,
			},
			expectedLog: `level=INFO msg="Info message"` + "\n",
		},
		{
			name: testHappyPathSlog2,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: infoMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: `level=INFO msg="Info message" test=toto toto=test` + "\n",
		},
		{
			name: testHappyPathLegacy,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           infoMessage,
				keysAndValues: nil,
			},
			expectedLog: "INFO Info message" + "\n",
		},
		{
			name: testHappyPathLegacy2,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: infoMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: "INFO Info message test=toto toto=test" + "\n",
		},
		{
			name:   ffLoggerNilMessage,
			logger: nil,
			fields: fields{
				msg:           infoMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   ffLoggerNotConfiguredMessage,
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           infoMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := os.CreateTemp(t.TempDir(), "")
			require.NoError(t, err)

			defer func() {
				_ = os.Remove(file.Name())
			}()

			legacy := tt.logger != nil && tt.logger.LeveledLogger == nil && tt.logger.LegacyLogger != nil
			if tt.logger != nil && tt.logger.LeveledLogger != nil {
				tt.logger.LeveledLogger = slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{
					Level: slog.LevelDebug,
					ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
						if a.Key == slog.TimeKey {
							return slog.Attr{}
						}

						return a
					},
				}))
			}

			if tt.logger != nil && tt.logger.LegacyLogger != nil {
				tt.logger.LegacyLogger.SetOutput(file)
			}

			if tt.fields.keysAndValues != nil {
				tt.logger.Info(tt.fields.msg, tt.fields.keysAndValues...)
			} else {
				tt.logger.Info(tt.fields.msg)
			}

			content, err := os.ReadFile(file.Name())
			require.NoError(t, err)

			actual := string(content)

			if legacy {
				// The legacy logger prefixes the lines with the current time (yyyy/mm/dd hh:mm:ss ).
				const timestampLen = len(timestampSample)
				if len(actual) >= timestampLen {
					actual = actual[timestampLen:]
				}
			}

			assert.Equal(t, tt.expectedLog, actual)
		})
	}
}

func TestFFLogger_Debug(t *testing.T) {
	t.Parallel()

	type fields struct {
		msg           string
		keysAndValues []any
	}

	tests := []struct {
		logger      *fflog.FFLogger
		name        string
		expectedLog string
		fields      fields
	}{
		{
			name: testHappyPathSlog,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           debugMessage,
				keysAndValues: nil,
			},
			expectedLog: `level=DEBUG msg="Debug message"` + "\n",
		},
		{
			name: testHappyPathSlog2,
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: debugMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: `level=DEBUG msg="Debug message" test=toto toto=test` + "\n",
		},
		{
			name: testHappyPathLegacy,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           debugMessage,
				keysAndValues: nil,
			},
			expectedLog: "DEBUG Debug message" + "\n",
		},
		{
			name: testHappyPathLegacy2,
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: debugMessage,
				keysAndValues: []any{
					slog.String(test, toto),
					slog.String(toto, test),
				},
			},
			expectedLog: "DEBUG Debug message test=toto toto=test" + "\n",
		},
		{
			name:   ffLoggerNilMessage,
			logger: nil,
			fields: fields{
				msg:           debugMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   ffLoggerNotConfiguredMessage,
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           debugMessage,
				keysAndValues: nil,
			},
			expectedLog: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			slog.SetLogLoggerLevel(slog.LevelDebug)

			file, err := os.CreateTemp(t.TempDir(), "")
			require.NoError(t, err)

			defer func() {
				_ = os.Remove(file.Name())
			}()

			legacy := tt.logger != nil && tt.logger.LeveledLogger == nil && tt.logger.LegacyLogger != nil
			if tt.logger != nil && tt.logger.LeveledLogger != nil {
				tt.logger.LeveledLogger = slog.New(slog.NewTextHandler(file, &slog.HandlerOptions{
					Level: slog.LevelDebug,
					ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
						if a.Key == slog.TimeKey {
							return slog.Attr{}
						}

						return a
					},
				}))
			}

			if tt.logger != nil && tt.logger.LegacyLogger != nil {
				tt.logger.LegacyLogger.SetOutput(file)
			}

			if tt.fields.keysAndValues != nil {
				tt.logger.Debug(tt.fields.msg, tt.fields.keysAndValues...)
			} else {
				tt.logger.Debug(tt.fields.msg)
			}

			content, err := os.ReadFile(file.Name())
			require.NoError(t, err)

			actual := string(content)

			if legacy {
				// The legacy logger prefixes the lines with the current time (yyyy/mm/dd hh:mm:ss ).
				const timestampLen = len(timestampSample)
				if len(actual) >= timestampLen {
					actual = actual[timestampLen:]
				}
			}

			assert.Equal(t, tt.expectedLog, actual)
		})
	}
}

func TestConvertToFFLogger(t *testing.T) {
	t.Parallel()

	l := log.New(os.Stdout, "", 0)
	ffl := fflog.ConvertToFFLogger(l)
	assert.Equal(t, ffl.GetLogLogger(slog.LevelInfo), l)
}

func TestGetLogLogger(t *testing.T) {
	t.Parallel()

	stdLogger := log.New(os.Stdout, "", 0)
	ffl := &fflog.FFLogger{
		LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
		LegacyLogger:  stdLogger,
	}

	ffl2 := &fflog.FFLogger{
		LegacyLogger: stdLogger,
	}

	assert.NotEqual(t, ffl.GetLogLogger(slog.LevelInfo), stdLogger)
	assert.Equal(t, ffl2.GetLogLogger(slog.LevelInfo), stdLogger)
}

func TestFFLogger_ContextMethods(t *testing.T) {
	t.Parallel()

	const key = ctxKey("req-id")

	type methodCall func(l *fflog.FFLogger, ctx context.Context, msg string, args ...any)

	tests := []struct {
		call        methodCall
		name        string
		msg         string
		wantLog     string
		wantCtxVal  any
		args        []any
		useLegacy   bool
		nilLogger   bool
		emptyLogger bool
		nilCtx      bool
	}{
		{
			name:       "ErrorContext with slog",
			call:       (*fflog.FFLogger).ErrorContext,
			msg:        "err msg",
			args:       []any{slog.String("k", "v")},
			wantLog:    `level=ERROR msg="err msg" k=v` + "\n",
			wantCtxVal: req1,
		},
		{
			name:       "WarnContext with slog",
			call:       (*fflog.FFLogger).WarnContext,
			msg:        "warn msg",
			wantLog:    `level=WARN msg="warn msg"` + "\n",
			wantCtxVal: req1,
		},
		{
			name:       "InfoContext with slog",
			call:       (*fflog.FFLogger).InfoContext,
			msg:        "info msg",
			wantLog:    `level=INFO msg="info msg"` + "\n",
			wantCtxVal: req1,
		},
		{
			name:       "DebugContext with slog",
			call:       (*fflog.FFLogger).DebugContext,
			msg:        "debug msg",
			wantLog:    `level=DEBUG msg="debug msg"` + "\n",
			wantCtxVal: req1,
		},
		{
			name:    "ErrorContext with nil ctx falls back to background",
			call:    (*fflog.FFLogger).ErrorContext,
			msg:     "err nil",
			nilCtx:  true,
			wantLog: `level=ERROR msg="err nil"` + "\n",
		},
		{
			name:    "WarnContext with nil ctx falls back to background",
			call:    (*fflog.FFLogger).WarnContext,
			msg:     "warn nil",
			nilCtx:  true,
			wantLog: `level=WARN msg="warn nil"` + "\n",
		},
		{
			name:    "InfoContext with nil ctx falls back to background",
			call:    (*fflog.FFLogger).InfoContext,
			msg:     "info nil",
			nilCtx:  true,
			wantLog: `level=INFO msg="info nil"` + "\n",
		},
		{
			name:    "DebugContext with nil ctx falls back to background",
			call:    (*fflog.FFLogger).DebugContext,
			msg:     "debug nil",
			nilCtx:  true,
			wantLog: `level=DEBUG msg="debug nil"` + "\n",
		},
		{
			name:      "ErrorContext with legacy logger",
			call:      (*fflog.FFLogger).ErrorContext,
			msg:       "err legacy",
			useLegacy: true,
			wantLog:   "ERROR err legacy\n",
		},
		{
			name:      "WarnContext with legacy logger",
			call:      (*fflog.FFLogger).WarnContext,
			msg:       "warn legacy",
			useLegacy: true,
			wantLog:   "WARN warn legacy\n",
		},
		{
			name:      "InfoContext with legacy logger",
			call:      (*fflog.FFLogger).InfoContext,
			msg:       "info legacy",
			useLegacy: true,
			wantLog:   "INFO info legacy\n",
		},
		{
			name:      "DebugContext with legacy logger",
			call:      (*fflog.FFLogger).DebugContext,
			msg:       "debug legacy",
			useLegacy: true,
			wantLog:   "DEBUG debug legacy\n",
		},
		{
			name:      "nil FFLogger does not panic",
			call:      (*fflog.FFLogger).ErrorContext,
			msg:       "ignored",
			nilLogger: true,
			wantLog:   "",
		},
		{
			name:        "empty FFLogger does not panic",
			call:        (*fflog.FFLogger).InfoContext,
			msg:         "ignored",
			emptyLogger: true,
			wantLog:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := os.CreateTemp(t.TempDir(), "")
			require.NoError(t, err)

			defer func() {
				_ = os.Remove(file.Name())
			}()

			var (
				logger  *fflog.FFLogger
				capture *ctxCaptureHandler
			)

			switch {
			case tt.nilLogger:
				logger = nil
			case tt.emptyLogger:
				logger = &fflog.FFLogger{}
			case tt.useLegacy:
				logger = &fflog.FFLogger{LegacyLogger: log.New(file, "", 0)}
			default:
				capture = &ctxCaptureHandler{
					key: key,
					Handler: slog.NewTextHandler(file, &slog.HandlerOptions{
						Level: slog.LevelDebug,
						ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
							if a.Key == slog.TimeKey {
								return slog.Attr{}
							}

							return a
						},
					}),
				}
				logger = &fflog.FFLogger{LeveledLogger: slog.New(capture)}
			}

			var ctx context.Context
			if !tt.nilCtx {
				ctx = context.WithValue(t.Context(), key, req1)
			}

			tt.call(logger, ctx, tt.msg, tt.args...)

			content, err := os.ReadFile(file.Name())
			require.NoError(t, err)

			actual := string(content)

			if tt.useLegacy {
				const timestampLen = len(timestampSample)
				if len(actual) >= timestampLen {
					actual = actual[timestampLen:]
				}
			}

			assert.Equal(t, tt.wantLog, actual)

			if capture != nil {
				assert.True(t, capture.nonNil)
				assert.Equal(t, tt.wantCtxVal, capture.gotVal)
			}
		})
	}
}
