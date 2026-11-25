package helper_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

var (
	errBoom = errors.New("boom")
)

func TestOutput_PrintLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		expected string
		output   helper.Output
	}{
		{
			name: "print info level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Info message", Level: helper.InfoLevel},
				},
			},
			expected: "INFO: Info message\n",
		},
		{
			name: "print warning level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Warning message", Level: helper.WarnLevel},
				},
			},
			expected: "WARNING: Warning message\n",
		},
		{
			name: "print error level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Error message", Level: helper.ErrorLevel},
				},
			},
			expected: "ERROR: Error message\n",
		},
		{
			name: "print default level line",
			output: helper.Output{
				Lines: []helper.OutputLine{
					{Text: "Default message", Level: helper.DefaultLevel},
				},
			},
			expected: "Default message\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			cmd := &cobra.Command{}
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)

			tt.output.PrintLines(cmd)
			assert.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestPrintFatal(t *testing.T) {
	t.Parallel()

	var (
		buf     bytes.Buffer
		gotCode int
	)

	called := false

	helper.PrintFatal(pterm.Error.WithWriter(&buf), func(code int) {
		gotCode = code
		called = true
	}, errBoom)

	assert.True(t, called, "the exit function should have been called")
	assert.Equal(t, 1, gotCode)
	assert.Equal(t, "ERROR: error executing command: boom\n", buf.String())
}

func TestLegacyWarner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		kinds     []string
		wantLines []string
	}{
		{
			name:      "one kind several times is logged once",
			kinds:     []string{"flag-file", "flag-file"},
			wantLines: []string{`{"level":"WARN","msg":"legacy name flag-file","legacyName":"flag-file"}`},
		},
		{
			name:  "two kinds are logged once each",
			kinds: []string{"flag-file", "config-file"},
			wantLines: []string{
				`{"level":"WARN","msg":"legacy name flag-file","legacyName":"flag-file"}`,
				`{"level":"WARN","msg":"legacy name config-file","legacyName":"config-file"}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
				ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
					if len(groups) == 0 && a.Key == slog.TimeKey {
						return slog.Attr{}
					}

					return a
				},
			}))

			w := helper.LegacyWarnerTo(func() *slog.Logger { return logger })
			for _, k := range tt.kinds {
				w.Warn(k, "legacy name "+k)
			}

			assert.Equal(t, strings.Join(tt.wantLines, "\n")+"\n", buf.String())
		})
	}
}

func TestOutput_Add(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		lines     []helper.OutputLine
		wantLines []helper.OutputLine
	}{
		{name: "no line", lines: nil, wantLines: nil},
		{
			name: "lines are kept in order",
			lines: []helper.OutputLine{
				{Text: "first", Level: helper.InfoLevel},
				{Text: "second", Level: helper.ErrorLevel},
			},
			wantLines: []helper.OutputLine{
				{Text: "first", Level: helper.InfoLevel},
				{Text: "second", Level: helper.ErrorLevel},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var o helper.Output
			for _, l := range tt.lines {
				o.Add(l.Text, l.Level)
			}

			assert.Equal(t, tt.wantLines, o.Lines)
		})
	}
}
