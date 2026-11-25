package cmdlog_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	fatal  = "fatal"
	logfmt = "logfmt"
	warn   = "warn"
)

// goldenTime returns the time of the records of the golden files.
func goldenTime() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 678901234, time.UTC) }

var (
	errBoom = errors.New("boom")
	errFail = errors.New("fail")
)

type stringer struct{}

func (stringer) String() string { return "stringer-value" }

type connection struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

// goldenRecords are the records logged to produce the golden files, with the zap based logger that the slog
// handler replaces (see testdata/zap_json.golden and testdata/zap_logfmt.golden). The order is the one of the files.
func goldenRecords() []struct {
	msg   string
	attrs []slog.Attr
	with  []slog.Attr
	level slog.Level
} {
	return []struct {
		msg   string
		attrs []slog.Attr
		with  []slog.Attr
		level slog.Level
	}{
		{msg: "plain message", level: slog.LevelInfo},
		{msg: "debug message", level: slog.LevelDebug},
		{msg: "warn message", level: slog.LevelWarn},
		{msg: "error message", level: slog.LevelError},
		{msg: "all types", level: slog.LevelInfo, attrs: []slog.Attr{
			slog.String("s", "value"), slog.Int("i", -42), slog.Int64("i64", 1<<40), slog.Bool("b", true),
			slog.Float64("f", 1.5), slog.Duration("d", 1500*time.Millisecond), slog.Time("t", goldenTime()),
			cmdlog.Err(errBoom), slog.String("str", stringer{}.String()),
			slog.Any("list", []string{"a", "b c"}), slog.Any("bytes", []byte("raw body")),
		}},
		{msg: "special chars", level: slog.LevelInfo, attrs: []slog.Attr{
			//nolint:gosmopolitan // non-ASCII input tests UTF-8 handling
			slog.String("s", "quote\" back\\ nl\n tab\t unicode é 日本"), slog.String("empty", ""),
			slog.String("space", "a b"), slog.String("eq", "a=b"),
		}},
		{msg: "with attrs", level: slog.LevelInfo, with: []slog.Attr{slog.String("flagset", "fs1")},
			attrs: []slog.Attr{slog.Int("n", 1)}},
		{msg: fixtures.Request, level: slog.LevelInfo, attrs: []slog.Attr{
			slog.String("remote_ip", "10.0.0.1"),
			slog.String("latency", "1.5ms"),
			slog.String(
				"host",
				"localhost:1031",
			),
			slog.String(fixtures.Request, "GET /v1/feature/x/eval?apiKey=REDACTED"),
			slog.Int("status", 200),
			slog.Int64("size", 12),
			slog.String("user_agent", "curl/8"),
			slog.String("request_id", "abc"),
		}},
		{msg: "with error", level: slog.LevelError, attrs: []slog.Attr{cmdlog.Err(errFail)}},
		{msg: "any struct", level: slog.LevelInfo, attrs: []slog.Attr{slog.Any("connection", connection{"x", 2})}},
		{msg: "any map", level: slog.LevelInfo, attrs: []slog.Attr{slog.Any("m", map[string]any{"k": 1})}},
		{msg: "nil error", level: slog.LevelInfo, attrs: []slog.Attr{cmdlog.Err(nil)}},
	}
}

var (
	callerJSON   = regexp.MustCompile(`"caller":"[^"]*"`)
	callerLogfmt = regexp.MustCompile(`caller=\S+`)
	stackJSON    = regexp.MustCompile(`,"stacktrace":"[^"]*"`)
	stackLogfmt  = regexp.MustCompile(` stacktrace="[^"]*"`)
)

// normalize removes what is expected to differ from the golden files: the caller (the golden files are
// captured from another file) and the stack trace of the error records (which the slog handler does not write).
func normalize(s string) string {
	s = callerJSON.ReplaceAllString(s, `"caller":"X"`)
	s = callerLogfmt.ReplaceAllString(s, `caller=X`)
	s = stackJSON.ReplaceAllString(s, ``)

	return stackLogfmt.ReplaceAllString(s, ``)
}

func TestGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantDifferent map[int]string
		name          string
		format        string
		file          string
	}{
		{
			name: protocol.FormatJSON, format: protocol.FormatJSON, file: "testdata/zap_json.golden",
		},
		{
			name: logfmt, format: logfmt, file: "testdata/zap_logfmt.golden",
			wantDifferent: map[int]string{
				// zap-logfmt could not encode these (`connection= connectionError="unsupported value type"`).
				9:  `ts=1767323045.6789012 level=info caller=X msg="any struct" connection="{\"name\":\"x\",\"n\":2}"`,
				10: `ts=1767323045.6789012 level=info caller=X msg="any map" m="{\"k\":1}"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			golden, err := os.ReadFile(tt.file)
			require.NoError(t, err)

			wantLines := strings.Split(strings.TrimSuffix(normalize(string(golden)), "\n"), "\n")

			var out bytes.Buffer

			logger, err := cmdlog.New(tt.format, fixtures.Debug, cmdlog.WithWriter(&out))
			require.NoError(t, err)

			records := goldenRecords()
			require.Len(t, wantLines, len(records))

			for _, rec := range records {
				handler := logger.Slog.Handler()
				if len(rec.with) > 0 {
					handler = handler.WithAttrs(rec.with)
				}

				r := slog.NewRecord(goldenTime(), rec.level, rec.msg, callerPC(t))
				r.AddAttrs(rec.attrs...)
				require.NoError(t, handler.Handle(context.Background(), r))
			}

			gotLines := strings.Split(strings.TrimSuffix(normalize(out.String()), "\n"), "\n")
			require.Len(t, gotLines, len(wantLines))

			for i := range wantLines {
				want := wantLines[i]
				if d, ok := tt.wantDifferent[i]; ok {
					want = d
				}

				assert.Equal(t, want, gotLines[i], "record %d", i)
			}
		})
	}
}

// callerPC returns the program counter of the caller of the test helper.
func callerPC(t *testing.T) uintptr {
	t.Helper()

	var pcs [1]uintptr
	require.Equal(t, 1, runtime.Callers(2, pcs[:]))

	return pcs[0]
}

// here returns the line of its caller. It is derived from the runtime, so the test does not read its source file
// (under -trimpath the file name is a module path, not a file on disk).
//
//go:noinline
func here() int {
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	frame, _ := runtime.CallersFrames(pcs[:]).Next()

	return frame.Line
}

// logHere is small enough to be inlined into its caller: the caller field must still be the line of the record.
// It returns the line of the record it logged.
func logHere(l *slog.Logger) int {
	line := here() + 2 // skip the blank line before LogAttrs

	l.LogAttrs(context.Background(), slog.LevelInfo, "inlined")

	return line
}

func TestCallerField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		format     string
		wantFormat string
	}{
		{name: protocol.FormatJSON, format: protocol.FormatJSON, wantFormat: `"caller":"cmdlog/logger_test.go:%d"`},
		{name: logfmt, format: logfmt, wantFormat: `caller=cmdlog/logger_test.go:%d `},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			logger, err := cmdlog.New(tt.format, fixtures.Info, cmdlog.WithWriter(&out))
			require.NoError(t, err)

			directLine := here() + 2 // skip the blank line before LogAttrs

			logger.Slog.LogAttrs(context.Background(), slog.LevelInfo, "direct")
			inlinedLine := logHere(logger.Slog)

			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			require.Len(t, lines, 2)
			assert.Contains(t, lines[0], fmt.Sprintf(tt.wantFormat, directLine))
			assert.Contains(t, lines[1], fmt.Sprintf(tt.wantFormat, inlinedLine))
		})
	}
}

func TestLevels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr   error
		name      string
		level     string
		wantShown []string
	}{
		{
			name:      fixtures.Debug,
			level:     fixtures.Debug,
			wantShown: []string{fixtures.Debug, fixtures.Info, warn, fixtures.Error, fatal},
		},
		{name: "empty is info", level: "", wantShown: []string{fixtures.Info, warn, fixtures.Error, fatal}},
		{name: "upper case", level: "WARN", wantShown: []string{warn, fixtures.Error, fatal}},
		{name: fixtures.Error, level: fixtures.Error, wantShown: []string{fixtures.Error, fatal}},
		{name: fatal, level: fatal, wantShown: []string{fatal}},
		{name: "dpanic", level: "dpanic", wantShown: []string{fatal}},
		{
			name:      "mixed case",
			level:     "DeBuG",
			wantShown: []string{fixtures.Debug, fixtures.Info, warn, fixtures.Error, fatal},
		},
		{name: "warning alias", level: "warning", wantShown: []string{warn, fixtures.Error, fatal}},
		{name: "upper warning alias", level: "WARNING", wantShown: []string{warn, fixtures.Error, fatal}},
		{name: "mixed case panic", level: "PaNiC", wantShown: []string{fatal}},
		{name: "unknown", level: "verbose", wantErr: cmdlog.ErrUnrecognizedLevel},
		{name: "padded", level: " info", wantErr: cmdlog.ErrUnrecognizedLevel},
		{name: "numeric", level: "1", wantErr: cmdlog.ErrUnrecognizedLevel},
		{name: "warnings", level: "warnings", wantErr: cmdlog.ErrUnrecognizedLevel},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			logger, err := cmdlog.New(protocol.FormatJSON, tt.level, cmdlog.WithWriter(&out))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)

			ctx := context.Background()
			for _, l := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError, cmdlog.LevelFatal} {
				logger.Slog.LogAttrs(ctx, l, "m")
			}

			var shown []string

			for line := range strings.SplitSeq(strings.TrimSpace(out.String()), "\n") {
				if line == "" {
					continue
				}

				shown = append(shown, regexp.MustCompile(`"level":"(\w+)"`).FindStringSubmatch(line)[1])
			}

			assert.Equal(t, tt.wantShown, shown)
		})
	}
}

func TestNewErrors(t *testing.T) {
	t.Parallel()

	_, err := cmdlog.New("yaml", fixtures.Info)
	require.ErrorIs(t, err, cmdlog.ErrUnknownFormat)
}

func TestUpdate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		name       string
		format     string
		level      string
		wantOut    string
		wantAbsent string
	}{
		{name: "logfmt and warn", format: logfmt, level: warn, wantOut: `level=warn`, wantAbsent: `info`},
		{name: "json by default", format: "", level: fixtures.Debug, wantOut: `"level":"debug"`, wantAbsent: `level=`},
		{name: "unknown format keeps the logger", format: "xml", level: fixtures.Debug, wantErr: cmdlog.ErrUnknownFormat,
			wantOut: `"level":"info"`},
		{name: "unknown level keeps the logger", format: logfmt, level: "x", wantErr: cmdlog.ErrUnrecognizedLevel,
			wantOut: `"level":"info"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			logger, err := cmdlog.New(protocol.FormatJSON, fixtures.Info, cmdlog.WithWriter(&out))
			require.NoError(t, err)
			// a logger derived before the update follows it.
			derived := logger.Slog.With(slog.String("flagset", "fs"))

			err = logger.Update(tt.format, tt.level)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			ctx := context.Background()
			derived.LogAttrs(ctx, slog.LevelDebug, "debugging")
			derived.LogAttrs(ctx, slog.LevelInfo, "hello")
			derived.LogAttrs(ctx, slog.LevelWarn, "danger")

			assert.Contains(t, out.String(), tt.wantOut)

			if tt.wantAbsent != "" {
				assert.NotContains(t, out.String(), tt.wantAbsent)
			}
		})
	}
}

func TestWithGroupAndGroupAttrs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		logger func(*slog.Logger) *slog.Logger
		name   string
		format string
		want   string
		attrs  []slog.Attr
	}{
		{
			name:   "json group attr",
			format: protocol.FormatJSON,
			logger: func(l *slog.Logger) *slog.Logger { return l },
			attrs:  []slog.Attr{slog.Group("g", slog.Int("a", 1), slog.String("b", "x"))},
			want:   `"msg":"m","g.a":1,"g.b":"x"}`,
		},
		{
			name: "logfmt WithGroup", format: logfmt,
			logger: func(l *slog.Logger) *slog.Logger { return l.WithGroup("req").With(slog.Int("id", 3)) },
			attrs:  []slog.Attr{slog.String("k", "v")},
			want:   `msg=m req.id=3 req.k=v`,
		},
		{
			name: "empty group and empty name are dropped", format: protocol.FormatJSON,
			logger: func(l *slog.Logger) *slog.Logger { return l.WithGroup("").With() },
			attrs:  []slog.Attr{slog.Group("g"), {}},
			want:   `"msg":"m"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			logger, err := cmdlog.New(tt.format, fixtures.Info, cmdlog.WithWriter(&out))
			require.NoError(t, err)

			tt.logger(logger.Slog).LogAttrs(context.Background(), slog.LevelInfo, "m", tt.attrs...)

			assert.Contains(t, out.String(), tt.want)
		})
	}
}

func TestValueKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		attr     slog.Attr
		wantJSON string
		wantLogf string
	}{
		{name: "uint64", attr: slog.Uint64("k", 7), wantJSON: `"k":7`, wantLogf: `k=7`},
		{name: "nan", attr: slog.Float64("k", nan()), wantJSON: `"k":"NaN"`, wantLogf: `k=NaN`},
		{name: "inf", attr: slog.Float64("k", inf(1)), wantJSON: `"k":"+Inf"`, wantLogf: `k=+Inf`},
		{name: "-inf", attr: slog.Float64("k", inf(-1)), wantJSON: `"k":"-Inf"`, wantLogf: `k=-Inf`},
		{name: "control char", attr: slog.String("k", "a\x01b\r"), wantJSON: `"k":"a\u0001b\r"`,
			wantLogf: `k="a\u0001b\r"`},
		{name: "invalid utf8", attr: slog.String("k", "a\xffb"), wantJSON: `"k":"a�b"`, wantLogf: `k="a�b"`},
		{name: "nil any", attr: slog.Any("k", nil), wantJSON: `"k":null`, wantLogf: `k=null`},
		{name: "unsupported any", attr: slog.Any("k", make(chan int)), wantJSON: `"k":"unsupported value type"`,
			wantLogf: `k="unsupported value type"`},
		{name: "empty list", attr: slog.Any("k", []string{}), wantJSON: `"k":[]`, wantLogf: `k=`},
		{name: "secret", attr: slog.Any("k", cmdlog.Secret("hunter2")), wantJSON: `"k":"[REDACTED]"`,
			wantLogf: `k=[REDACTED]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for format, want := range map[string]string{protocol.FormatJSON: tt.wantJSON, logfmt: tt.wantLogf} {
				var out bytes.Buffer

				logger, err := cmdlog.New(format, fixtures.Info, cmdlog.WithWriter(&out))
				require.NoError(t, err)

				logger.Slog.LogAttrs(context.Background(), slog.LevelInfo, "m", tt.attr)

				assert.Contains(t, out.String(), want, format)
			}
		})
	}
}

type failingWriter struct{}

var errWrite = errors.New("disk full")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestWriteError(t *testing.T) {
	t.Parallel()

	logger, err := cmdlog.New(protocol.FormatJSON, fixtures.Info, cmdlog.WithWriter(failingWriter{}))
	require.NoError(t, err)

	r := slog.NewRecord(goldenTime(), slog.LevelInfo, "m", 0)
	require.ErrorIs(t, logger.Slog.Handler().Handle(context.Background(), r), errWrite)
}

func TestNoCallerWithoutPC(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	logger, err := cmdlog.New(protocol.FormatJSON, fixtures.Info, cmdlog.WithWriter(&out))
	require.NoError(t, err)

	r := slog.NewRecord(goldenTime(), slog.LevelInfo, "m", 0)
	require.NoError(t, logger.Slog.Handler().Handle(context.Background(), r))

	assert.JSONEq(t, `{"level":"info","ts":1767323045.6789012,"msg":"m"}`, out.String())
	assert.True(t, strings.HasSuffix(out.String(), "}\n"))
}

// syncWriter counts the lines written, to check that no record is torn or lost.
type syncWriter struct {
	mu    sync.Mutex
	lines int
}

func (w *syncWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.lines += bytes.Count(data, []byte("\n"))

	return len(data), nil
}

func TestConcurrentUse(t *testing.T) {
	t.Parallel()

	const goroutines, perGoroutine = 8, 200

	w := &syncWriter{}
	logger, err := cmdlog.New(protocol.FormatJSON, fixtures.Info, cmdlog.WithWriter(w))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for i := range perGoroutine {
				logger.Slog.LogAttrs(context.Background(), slog.LevelInfo, "m", slog.Int("i", i))

				if i%50 == 0 {
					assert.NoError(t, logger.Update(protocol.FormatJSON, fixtures.Info))
				}
			}
		})
	}

	wg.Wait()

	w.mu.Lock()
	defer w.mu.Unlock()

	assert.Equal(t, goroutines*perGoroutine, w.lines)
}

func nan() float64 { return math.NaN() }

func inf(sign int) float64 { return math.Inf(sign) }
