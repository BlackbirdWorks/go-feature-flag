package fflog_test

import (
	"log"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/utils/fflog"
)

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
			name: "Test Happy Path - slog",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Error message",
				keysAndValues: nil,
			},
			expectedLog: `level=ERROR msg="Error message"` + "\n",
		},
		{
			name: "Test Happy Path - slog with keys and values",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Error message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: `level=ERROR msg="Error message" test=toto toto=test` + "\n",
		},
		{
			name: "Test Happy Path - legacy",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Error message",
				keysAndValues: nil,
			},
			expectedLog: "ERROR Error message" + "\n",
		},
		{
			name: "Test Happy Path - legacy with keys and values",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Error message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: "ERROR Error message test=toto toto=test" + "\n",
		},
		{
			name:   "FFLogger nil",
			logger: nil,
			fields: fields{
				msg:           "Error message",
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   "FFLogger no logger configured",
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           "Error message",
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
				const timestampLen = len("2006/01/02 15:04:05 ")
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
			name: "Test Happy Path - slog",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Warn message",
				keysAndValues: nil,
			},
			expectedLog: `level=WARN msg="Warn message"` + "\n",
		},
		{
			name: "Test Happy Path - slog with keys and values",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Warn message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: `level=WARN msg="Warn message" test=toto toto=test` + "\n",
		},
		{
			name: "Test Happy Path - legacy",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Warn message",
				keysAndValues: nil,
			},
			expectedLog: "WARN Warn message" + "\n",
		},
		{
			name: "Test Happy Path - legacy with keys and values",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Warn message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: "WARN Warn message test=toto toto=test" + "\n",
		},
		{
			name:   "FFLogger nil",
			logger: nil,
			fields: fields{
				msg:           "Warn message",
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   "FFLogger no logger configured",
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           "Warn message",
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
				const timestampLen = len("2006/01/02 15:04:05 ")
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
			name: "Test Happy Path - slog",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Info message",
				keysAndValues: nil,
			},
			expectedLog: `level=INFO msg="Info message"` + "\n",
		},
		{
			name: "Test Happy Path - slog with keys and values",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Info message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: `level=INFO msg="Info message" test=toto toto=test` + "\n",
		},
		{
			name: "Test Happy Path - legacy",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Info message",
				keysAndValues: nil,
			},
			expectedLog: "INFO Info message" + "\n",
		},
		{
			name: "Test Happy Path - legacy with keys and values",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Info message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: "INFO Info message test=toto toto=test" + "\n",
		},
		{
			name:   "FFLogger nil",
			logger: nil,
			fields: fields{
				msg:           "Info message",
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   "FFLogger no logger configured",
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           "Info message",
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
				const timestampLen = len("2006/01/02 15:04:05 ")
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
			name: "Test Happy Path - slog",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Debug message",
				keysAndValues: nil,
			},
			expectedLog: `level=DEBUG msg="Debug message"` + "\n",
		},
		{
			name: "Test Happy Path - slog with keys and values",
			logger: &fflog.FFLogger{
				LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
				LegacyLogger:  log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Debug message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: `level=DEBUG msg="Debug message" test=toto toto=test` + "\n",
		},
		{
			name: "Test Happy Path - legacy",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg:           "Debug message",
				keysAndValues: nil,
			},
			expectedLog: "DEBUG Debug message" + "\n",
		},
		{
			name: "Test Happy Path - legacy with keys and values",
			logger: &fflog.FFLogger{
				LegacyLogger: log.New(os.Stdout, "", 0),
			},
			fields: fields{
				msg: "Debug message",
				keysAndValues: []any{
					slog.String("test", "toto"),
					slog.String("toto", "test"),
				},
			},
			expectedLog: "DEBUG Debug message test=toto toto=test" + "\n",
		},
		{
			name:   "FFLogger nil",
			logger: nil,
			fields: fields{
				msg:           "Debug message",
				keysAndValues: nil,
			},
			expectedLog: "",
		},
		{
			name:   "FFLogger no logger configured",
			logger: &fflog.FFLogger{},
			fields: fields{
				msg:           "Debug message",
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
				const timestampLen = len("2006/01/02 15:04:05 ")
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

	l := log.New(os.Stdout, "", 0)
	ffl := &fflog.FFLogger{
		LeveledLogger: slog.New(slog.DiscardHandler), // replaced by a per-case handler writing to the case file
		LegacyLogger:  l,
	}

	ffl2 := &fflog.FFLogger{
		LegacyLogger: l,
	}

	assert.NotEqual(t, ffl.GetLogLogger(slog.LevelInfo), l)
	assert.Equal(t, ffl2.GetLogLogger(slog.LevelInfo), l)
}
