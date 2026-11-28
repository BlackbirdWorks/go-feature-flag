//go:build docker

package docstest_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	e2eTimeout = 5 * time.Minute
	sentinel   = "@@docs-test-block@@"
	emulator   = "semaphore-s3"
)

// build compiles a command of the repository into dir.
func build(ctx context.Context, t *testing.T, dir, name, pkg string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(dir, name), pkg)
	cmd.Dir = "../.."

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// leftovers removes what a failed run leaves behind: the emulator container and the relay process of the page.
func leftovers(ctx context.Context, work string) {
	_ = exec.CommandContext(context.WithoutCancel(ctx), "docker", "rm", "-f", emulator).Run()

	pid, err := strconv.Atoi(readTrimmed(filepath.Join(work, "relay.pid")))
	if err != nil {
		return
	}

	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Signal(syscall.SIGTERM)
	}
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(raw))
}

// The getting-started page is followed literally: every shell block of the page runs, in order, in one shell, against
// the binaries built from this tree and a gopherstack emulator that the page starts itself. The text block after a
// shell block is what that block prints.
func TestGettingStartedRunsAsWritten(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), e2eTimeout)
	t.Cleanup(cancel)

	bin, work := t.TempDir(), t.TempDir()

	build(ctx, t, bin, "semaphore", "./cmd/relayproxy")
	build(ctx, t, bin, "semaphore-cli", "./cmd/cli")

	var script strings.Builder

	script.WriteString("set -euo pipefail\n")

	var wantOutputs []string // the expected output of each run block, empty when the page shows none

	blocks := fences(t, gettingPage)
	for i, block := range blocks {
		if block.marker != markerRun {
			continue
		}

		script.WriteString(block.body + "\necho " + sentinel + "\n")

		want := ""
		if i+1 < len(blocks) && blocks[i+1].marker == markerOutput {
			want = strings.TrimSpace(blocks[i+1].body)
		}

		wantOutputs = append(wantOutputs, want)
	}

	require.NotEmpty(t, wantOutputs)

	t.Cleanup(func() { leftovers(ctx, work) })

	var stdout, stderr bytes.Buffer

	cmd := exec.CommandContext(ctx, "bash", "-c", script.String())
	cmd.Dir = work

	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()
	require.NoError(t, err, "stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())

	got := strings.Split(stdout.String(), sentinel+"\n")
	require.Len(t, got, len(wantOutputs)+1, "one output per run block")

	for i, want := range wantOutputs {
		if want == "" {
			continue
		}

		t.Run("block "+strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, want, strings.TrimSpace(got[i]))
		})
	}
}
