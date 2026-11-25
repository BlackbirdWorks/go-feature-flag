package testutils

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// CopyFileToNewTempFile copies the file src into a new temporary file and returns it.
func CopyFileToNewTempFile(t *testing.T, src string) *os.File {
	t.Helper()

	_, statErr := os.Stat(src)
	if os.IsNotExist(statErr) {
		require.Fail(t, "File does not exist: "+src)
	}

	srcContent, err := os.ReadFile(src)
	require.NoError(t, err)

	dir := t.TempDir()
	file, err := os.CreateTemp(dir, "")
	require.NoError(t, err)

	defer func() {
		_ = file.Close()
	}()

	err = os.WriteFile(file.Name(), srcContent, 0o600) //nolint:gosec // G703 - path from CreateTemp is safe
	require.NoError(t, err)
	syncFile(t, file.Name())

	return file
}

// CopyContentToNewTempFile writes content into a new temporary file and returns it.
func CopyContentToNewTempFile(t *testing.T, content string) *os.File {
	t.Helper()
	dir := t.TempDir()
	file, err := os.CreateTemp(dir, "")
	require.NoError(t, err)

	defer func() {
		_ = file.Close()
	}()

	err = os.WriteFile(file.Name(), []byte(content), 0o600)
	require.NoError(t, err)
	syncFile(t, file.Name())

	return file
}

// CopyContentToExistingTempFile writes content into file and returns it.
func CopyContentToExistingTempFile(t *testing.T, content string, file *os.File) *os.File {
	t.Helper()

	err := os.WriteFile(file.Name(), []byte(content), 0o600) //nolint:gosec // G703 - path from caller's temp file

	require.NoError(t, err)
	syncFile(t, file.Name())

	return file
}

// CopyFileToExistingTempFile copies the file src into file and returns it.
func CopyFileToExistingTempFile(t *testing.T, src string, file *os.File) *os.File {
	t.Helper()

	_, statErr := os.Stat(src)
	if os.IsNotExist(statErr) {
		require.Fail(t, "File does not exist: "+src)
	}

	srcContent, err := os.ReadFile(src)
	require.NoError(t, err)

	return CopyContentToExistingTempFile(t, string(srcContent), file)
}

// ReplaceInFile replaces the text old with newStr in file.
func ReplaceInFile(t *testing.T, file *os.File, old, newStr string) {
	t.Helper()

	content, err := os.ReadFile(file.Name())
	require.NoError(t, err)

	updated := strings.Replace(string(content), old, newStr, 1)
	err = os.WriteFile(file.Name(), []byte(updated), 0o600) //nolint:gosec // G703 - path from caller's file is safe
	require.NoError(t, err)
	syncFile(t, file.Name())
}

// ReplaceAndCopyFileToExistingFile copies src into dstFile, replacing old with newStr, and returns dstFile.
func ReplaceAndCopyFileToExistingFile(t *testing.T, src string, dstFile *os.File, old, newStr string) *os.File {
	t.Helper()

	srcContent, err := os.ReadFile(src)
	require.NoError(t, err)

	updatedContent := strings.Replace(string(srcContent), old, newStr, 1)

	return CopyContentToExistingTempFile(t, updatedContent, dstFile)
}

// syncFile ensures the file is written to disk before returning.
// This is important for file watchers that might detect changes before the file is fully written.
// Without syncing, the file watcher might detect the change before the OS has flushed the write,
// causing the reload to read stale or empty file content.
func syncFile(t *testing.T, filePath string) {
	t.Helper()

	file, err := os.OpenFile(filePath, os.O_RDWR, 0o600)
	require.NoError(t, err, "failed to open file for syncing: %s", filePath)
	err = file.Sync()
	require.NoError(t, err, "failed to sync file: %s", filePath)
	err = file.Close()
	require.NoError(t, err, "failed to close file after syncing: %s", filePath)
}
