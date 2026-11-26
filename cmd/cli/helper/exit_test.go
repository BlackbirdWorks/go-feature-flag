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
