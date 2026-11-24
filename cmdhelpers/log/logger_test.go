package log_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// logFile returns a log file of its own for the test and a function that reads what was logged.
func logFile(t *testing.T) (string, func() string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.log")

	return path, func() string {
		b, err := os.ReadFile(path)
		require.NoError(t, err)

		return string(b)
	}
}

func TestInitLogger(t *testing.T) {
	t.Parallel()

	path, read := logFile(t)

	logger := log.InitLogger(log.WithOutputPaths(path))
	logger.ZapLogger.Info("test message")

	out := read()
	assert.Contains(t, out, `"level":"info"`)
	assert.Contains(t, out, `"msg":"test message"`)
}

func TestLoggerUpdate(t *testing.T) {
	t.Parallel()

	t.Run("update to logfmt/warn", func(t *testing.T) {
		t.Parallel()

		path, read := logFile(t)

		logger := log.InitLogger(log.WithOutputPaths(path))
		logger.Update("logfmt", zap.WarnLevel)
		logger.ZapLogger.Info("hidden")
		logger.ZapLogger.Warn("danger, Will Robinson!")

		b := read()

		assert.NotContains(t, b, `{`)
		assert.NotContains(t, b, `info`)

		assert.Contains(t, b, `level=warn`)
		assert.Contains(t, b, `msg="danger, Will Robinson!"`)
	})

	t.Run("json by default", func(t *testing.T) {
		t.Parallel()

		path, read := logFile(t)

		logger := log.InitLogger(log.WithOutputPaths(path))
		logger.Update("", zap.DebugLevel)
		logger.ZapLogger.Info("hello world")
		logger.ZapLogger.Debug("debugging")

		b := read()

		assert.Contains(t, b, `"level":"debug"`)
		assert.Contains(t, b, `"msg":"debugging"`)
	})
}
