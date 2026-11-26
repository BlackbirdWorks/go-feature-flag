package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pterm/pterm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

func TestCmdExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantOut string
		args    []string
	}{
		{
			name:    "lint command should exist",
			args:    []string{"lint", "linter/testdata/valid.yaml", "--format", "yaml"},
			wantOut: "  INFO    Valid Semaphore configuration\n",
		},
		{
			name: "evaluate command should exist",
			args: []string{
				"evaluate", "--config", "evaluate/testdata/flag.goff.yaml",
				"--ctx", `{"targetingKey": "user-123"}`, "--format", "yaml",
			},
			wantOut: `Flag --config has been deprecated, Use path instead
{
  "simple-flag": {
    "trackEvents": true,
    "variationType": "B",
    "failed": false,
    "version": "",
    "reason": "STATIC",
    "errorCode": "",
    "value": true,
    "cacheable": true,
    "metadata": {
      "description": "this is a simple feature flag",
      "issue-link": "https://jira.xxx/GOFF-01"
    }
  },
  "test-flag": {
    "trackEvents": true,
    "variationType": "Default",
    "failed": false,
    "version": "",
    "reason": "DEFAULT",
    "errorCode": "",
    "value": false,
    "cacheable": true,
    "metadata": {
      "description": "this is a simple feature flag",
      "issue-link": "https://jira.xxx/GOFF-01"
    }
  },
  "test-flag2": {
    "trackEvents": true,
    "variationType": "Default",
    "failed": false,
    "version": "",
    "reason": "DEFAULT",
    "errorCode": "",
    "value": false,
    "cacheable": true
  }
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := initRootCmd(tt.args)

			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)
			require.NoError(t, cmd.Execute())
			assert.Equal(t, tt.wantOut, pterm.RemoveColorFromString(out.String()))
		})
	}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--version", "-v"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			cmd := initRootCmd([]string{flag})
			cmd.SetOut(&out)
			cmd.SetArgs([]string{flag})
			require.NoError(t, cmd.Execute())
			assert.Contains(t, out.String(), "semaphore-cli localdev\ncommit: ")
			assert.Contains(t, out.String(), "\nfips140: false\n")
		})
	}
}

const (
	cmdValidate    = "validate"
	cmdVerify      = "verify"
	codeUsage      = "usage"
	fatalLine      = "error executing command"
	unknownFlag    = "--not-a-flag"
	unknownFlagMsg = "unknown flag"
	usageText      = "Usage:"
	cmdConfig      = "config"
	cmdPublish     = "publish"
	fmtJSON        = "json"
	fmtText        = "text"
	optOutput      = "--output"
	flagFormat     = "--format"
	formatYAML     = "yaml"
)

func TestConfigAndPublishCommandsAreRegistered(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "bootstrap.yaml")
	require.NoError(
		t,
		os.WriteFile(file, []byte("version: 2\nconnections:\n  c: {type: s3, region: us-east-1, bucket: b}\n"), 0o600),
	)

	tests := []struct {
		name    string
		wantOut string
		args    []string
	}{
		{name: "config validate", args: []string{cmdConfig, cmdValidate, file}, wantOut: file + ": valid\n"},
		{
			name:    "config print",
			args:    []string{cmdConfig, "print", "--effective", file, "-o", fmtJSON},
			wantOut: `"bucket": "b"`,
		},
		{
			name:    "publish verify exists",
			args:    []string{cmdPublish, cmdVerify, "--help"},
			wantOut: "Reads the manifest and every listed object",
		},
		{name: "publish exists", args: []string{cmdPublish, "--help"}, wantOut: "--dry-run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := initRootCmd(tt.args)

			var out bytes.Buffer

			cmd.SetOut(&out)
			cmd.SetArgs(tt.args)
			require.NoError(t, cmd.Execute())
			assert.Contains(t, pterm.RemoveColorFromString(out.String()), tt.wantOut)
		})
	}
}

// helperEnv makes the test binary act as the command line: TestHelperProcess runs the arguments after "--".
const helperEnv = "SEMAPHORE_CLI_TEST_HELPER"

// TestHelperProcess is not a test: it is the entry point of the process that runCLI starts.
//
//nolint:paralleltest // the helper process runs one command line and exits
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("only runs as the helper process of runCLI")
	}

	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]

			break
		}
	}

	os.Exit(execute(args)) //nolint:revive // the helper process ends with the exit code of the command line
}

// runCLI runs the command line in a process of its own and returns what it wrote on each stream and its exit code.
func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.CommandContext(
		t.Context(),
		os.Args[0],
		append([]string{"-test.run=^TestHelperProcess$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), helperEnv+"=1",
		// static credentials for the explicit-target commands: nothing reaches a real AWS endpoint
		"AWS_ACCESS_KEY_ID=test", "AWS_SECRET_ACCESS_KEY=test", "AWS_EC2_METADATA_DISABLED=true",
		"AWS_CONFIG_FILE="+os.DevNull, "AWS_SHARED_CREDENTIALS_FILE="+os.DevNull)

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()

	code := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}

	return stdout.String(), stderr.String(), code
}

// verifyServer is a minimal S3 that holds a manifest listing one object that does not exist, so that "publish
// verify" has a problem to report.
func verifyServer(t *testing.T) *httptest.Server {
	t.Helper()

	sum := controlplane.ChecksumOf([]byte("x"))
	key, err := controlplane.ObjectKey(sum, controlplane.FormatYAML)
	require.NoError(t, err)

	manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
		Channel: "stable", Generation: 1,
		Objects: []controlplane.ManifestObject{{
			Name: "gone", Kind: controlplane.KindFlags, Connection: "control", Key: key,
			Format: controlplane.FormatYAML, Checksum: sum, Size: 1,
		}},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bucket/manifests/stable.yaml" {
			w.Header().Set("ETag", `"abc"`)
			_, _ = w.Write(manifest)

			return
		}

		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code><Message>no</Message></Error>`))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestWholeProcessStreams(t *testing.T) {
	t.Parallel()

	v1File := filepath.Join(t.TempDir(), "v1.yaml")
	require.NoError(t, os.WriteFile(v1File, []byte("port: 1031\n"), 0o600))

	missing := filepath.Join(t.TempDir(), "missing.yaml")
	srv := verifyServer(t)
	target := []string{"--bucket", "bucket", "--region", "us-east-1", "--endpoint", srv.URL}

	// One rule for every command: stdout carries results only (and in JSON mode exactly one JSON value), stderr
	// carries every diagnostic, including the final error line. An empty want is checked as exactly empty.
	tests := []struct {
		name       string
		wantStdout string
		wantStderr string
		jsonStdout string // the "error.code" of the one JSON error value, or "report" for a report
		args       []string
		wantExit   int
	}{
		{name: "version", args: []string{"--version"}, wantStdout: "semaphore-cli"},
		{name: "unknown root flag", args: []string{unknownFlag}, wantStderr: usageText, wantExit: 1},
		{name: "unknown subcommand", args: []string{"bogus"}, wantStderr: "unknown command", wantExit: 1},
		{
			name:       "lint failure",
			args:       []string{"lint", missing, flagFormat, formatYAML},
			wantStderr: "impossible to find",
			wantExit:   1,
		},
		{
			name:       "evaluate failure",
			args:       []string{"evaluate", "--path", missing, "--ctx", "{}", flagFormat, formatYAML},
			wantStderr: "no such file",
			wantExit:   1,
		},
		{
			name:       "config failure, argument count",
			args:       []string{cmdConfig, cmdValidate},
			wantStderr: "accepts 1 arg(s)",
			wantExit:   1,
		},
		{
			name:       "config failure, invalid file",
			args:       []string{cmdConfig, cmdValidate, v1File},
			wantStderr: "version 2 required",
			wantExit:   2,
		},
		{
			name: "publish unknown flag after -o json", args: []string{cmdPublish, unknownFlag, "-o", fmtJSON},
			wantStderr: unknownFlagMsg, jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name: "publish unknown flag before --output=json", args: []string{cmdPublish, unknownFlag, "--output=json"},
			wantStderr: unknownFlagMsg, jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name: "config unknown flag before -ojson", args: []string{cmdConfig, cmdValidate, unknownFlag, "-ojson"},
			wantStderr: unknownFlagMsg, jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name:       "publish unknown flag with -o text last wins",
			args:       []string{cmdPublish, unknownFlag, "-o", fmtJSON, "-o", fmtText},
			wantStderr: unknownFlagMsg,
			wantExit:   1,
		},
		{
			// Cobra parsed "--output json" before it stopped at the bad flag; the later "text" decides.
			name:       "publish json parsed, then a bad flag, then text",
			args:       []string{cmdPublish, optOutput, fmtJSON, unknownFlag, optOutput, fmtText},
			wantStderr: unknownFlagMsg,
			wantExit:   1,
		},
		{
			name: "publish text parsed, then a bad flag, then json",
			args: []string{
				cmdPublish,
				optOutput,
				fmtText,
				unknownFlag,
				optOutput,
				fmtJSON,
			},
			wantStderr: unknownFlagMsg,
			jsonStdout: codeUsage,
			wantExit:   1,
		},
		{
			name:       "config json parsed, then a bad flag, then text",
			args:       []string{cmdConfig, cmdValidate, "-o", fmtJSON, unknownFlag, "-o", fmtText},
			wantStderr: unknownFlagMsg,
			wantExit:   1,
		},
		{
			name: "config failure, JSON", args: []string{cmdConfig, "print", "--effective", v1File, "-o", fmtJSON},
			wantStderr: "version 2 required", jsonStdout: "invalid", wantExit: 2,
		},
		{
			name:       "publish failure, text",
			args:       []string{cmdPublish, t.TempDir()},
			wantStderr: "give --config, or --bucket",
			wantExit:   1,
		},
		{
			name: "publish failure, JSON", args: []string{cmdPublish, t.TempDir(), "-o", fmtJSON},
			wantStderr: "give --config, or --bucket", jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name: "publish usage error, JSON", args: []string{cmdPublish, "-o", fmtJSON},
			wantStderr: "accepts 1 arg(s)", jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name: "publish flag error, JSON", args: []string{cmdPublish, "-o", fmtJSON, unknownFlag},
			wantStderr: unknownFlagMsg, jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name: "verify with problems, text", args: append([]string{cmdPublish, cmdVerify}, target...),
			wantStdout: "problem: gone missing", wantStderr: "verification failed", wantExit: 5,
		},
		{
			name: "verify with problems, JSON", args: append([]string{cmdPublish, cmdVerify, "-o", fmtJSON}, target...),
			wantStderr: "verification failed", jsonStdout: "report", wantExit: 5,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdout, stderr, code := runCLI(t, tt.args...)
			stdout, stderr = pterm.RemoveColorFromString(stdout), pterm.RemoveColorFromString(stderr)

			assert.Equal(t, tt.wantExit, code, "stdout %q stderr %q", stdout, stderr)
			assert.NotContains(t, stdout, fatalLine, "the final error line is never on stdout")
			assert.NotContains(t, stdout, usageText, "usage is never on stdout")

			if tt.wantStderr == "" {
				assert.Empty(t, stderr)
			} else {
				assert.Contains(t, stderr, tt.wantStderr)
			}

			if tt.wantExit != 0 {
				assert.Contains(t, stderr, fatalLine, "every failure ends with the final error line on stderr")
			} else {
				assert.NotContains(t, stderr, fatalLine)
			}

			switch {
			case tt.jsonStdout != "":
				assertOneJSON(t, stdout, tt.jsonStdout)
			case tt.wantStdout == "":
				assert.Empty(t, stdout)
			default:
				assert.Contains(t, stdout, tt.wantStdout)
			}
		})
	}
}

// jsonDoc is the shape of what the whole-process tests read from stdout.
type jsonDoc struct {
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
	OK *bool `json:"ok"`
}

// decodeOneJSON decodes stdout as exactly one JSON value. Trailing whitespace is fine; a trailing delimiter, any
// other text or a second value is an error.
func decodeOneJSON(stdout string) (jsonDoc, error) {
	dec := json.NewDecoder(strings.NewReader(stdout))

	var doc jsonDoc

	err := dec.Decode(&doc)
	if err != nil {
		return doc, fmt.Errorf("first value: %w", err)
	}

	var extra json.RawMessage

	err = dec.Decode(&extra)
	if !errors.Is(err, io.EOF) {
		return doc, fmt.Errorf("%w after the first value: %w", errNotOneValue, err)
	}

	return doc, nil
}

var errNotOneValue = errors.New("stdout is not exactly one JSON value")

func TestDecodeOneJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		stdout  string
		wantErr bool
	}{
		{name: "one value", stdout: `{"error":{"code":"invalid"}}`},
		{name: "trailing whitespace is fine", stdout: "{\"ok\":false}\n\n  \t"},
		{name: "a trailing array terminator", stdout: `{"error":{"code":"invalid"}}]`, wantErr: true},
		{name: "a trailing brace", stdout: `{"ok":false}}`, wantErr: true},
		{name: "a second value", stdout: `{"ok":false}{"ok":true}`, wantErr: true},
		{name: "trailing text", stdout: "{\"ok\":false}\nERROR boom", wantErr: true},
		{name: "empty stdout", stdout: "", wantErr: true},
		{name: "not JSON", stdout: "ERROR boom", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeOneJSON(tt.stdout)
			assert.Equal(t, tt.wantErr, err != nil, "%v", err)
		})
	}
}

// assertOneJSON checks that stdout is exactly one JSON value: an error object with the given code, or a report.
func assertOneJSON(t *testing.T, stdout, want string) {
	t.Helper()

	doc, err := decodeOneJSON(stdout)
	require.NoError(t, err, "stdout: %q", stdout)

	if want == "report" {
		require.NotNil(t, doc.OK)
		assert.False(t, *doc.OK)
		assert.Nil(t, doc.Error, "a failed verification is its report, not an error object")

		return
	}

	require.NotNil(t, doc.Error)
	assert.Equal(t, want, doc.Error.Code)
}
