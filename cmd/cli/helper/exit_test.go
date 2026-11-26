package helper_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

func TestExitCodeOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err      error
		name     string
		wantCode int
	}{
		{name: "plain error", err: errBoom, wantCode: helper.ExitFailure},
		{name: "nil", err: nil, wantCode: helper.ExitFailure},
		{name: "conflict", err: helper.WithExitCode(errBoom, helper.ExitConflict), wantCode: helper.ExitConflict},
		{
			name:     "wrapped",
			err:      fmt.Errorf("outer: %w", helper.WithExitCode(errBoom, helper.ExitTransport)),
			wantCode: helper.ExitTransport,
		},
		{name: "zero code means failure", err: &helper.ExitError{Err: errBoom, Code: 0}, wantCode: helper.ExitFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantCode, helper.ExitCodeOf(tt.err))
		})
	}
}

func TestWithExitCode(t *testing.T) {
	t.Parallel()

	require.NoError(t, helper.WithExitCode(nil, helper.ExitInvalid))

	err := helper.WithExitCode(errBoom, helper.ExitInvalid)
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, "boom", err.Error())
}

func TestPrintFatalUsesTheExitCodeOfTheError(t *testing.T) {
	t.Parallel()

	var (
		buf  bytes.Buffer
		code int
	)

	helper.PrintFatal(
		pterm.Error.WithWriter(&buf),
		func(c int) { code = c },
		helper.WithExitCode(errBoom, helper.ExitConflict),
	)

	assert.Equal(t, helper.ExitConflict, code)
	assert.Contains(t, buf.String(), "boom")
}

func TestJSONErrorName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want string
		code int
	}{
		{code: helper.ExitFailure, want: "failure"},
		{code: helper.ExitInvalid, want: "invalid"},
		{code: helper.ExitConflict, want: "conflict"},
		{code: helper.ExitTransport, want: "transport"},
		{code: helper.ExitVerify, want: "bad-state"},
		{code: 99, want: "failure"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, helper.JSONErrorName(tt.code), "exit code %d", tt.code)
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errBoom }

func TestWriteJSONError(t *testing.T) {
	t.Parallel()

	t.Run("details are never null", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		require.NoError(t, helper.WriteJSONError(&buf, "invalid", errBoom, nil))
		assert.JSONEq(t, `{"error":{"code":"invalid","message":"boom","details":[]}}`, buf.String())
	})

	t.Run("details are kept in order", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		require.NoError(t, helper.WriteJSONError(&buf, "conflict", errBoom, []string{"b", "a"}))
		assert.JSONEq(t, `{"error":{"code":"conflict","message":"boom","details":["b","a"]}}`, buf.String())
	})

	t.Run("a failed write is an error", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, helper.WriteJSONError(brokenWriter{}, "invalid", errBoom, nil), errBoom)
	})
}

func TestFatalPrinterWritesToStderr(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	helper.PrintFatal(helper.FatalPrinter(&stderr), func(int) {}, errBoom)
	assert.Contains(t, stderr.String(), "boom")
}
