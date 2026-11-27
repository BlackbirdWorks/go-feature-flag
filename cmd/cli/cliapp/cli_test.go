package cliapp_test

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

	"github.com/blackbirdworks/semaphore/cmd/cli/cliapp"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	cmdValidate        = "validate"
	cmdVerify          = "verify"
	codeInvalid        = "invalid"
	codeUsage          = "usage"
	fatalLine          = "error executing command"
	unknownFlag        = "--not-a-flag"
	unknownFlagMsg     = "unknown flag"
	usageText          = "Usage:"
	cmdConfig          = "config"
	cmdPublish         = "publish"
	fmtJSON            = "json"
	fmtText            = "text"
	flagConfig         = "--config"
	optOutput          = "--output"
	optOutputJSON      = "--output=json"
	flagFormat         = "--format"
	formatYAML         = "yaml"
	helperEnv          = "SEMAPHORE_CLI_TEST_HELPER"
	localDevVersion    = "localdev"
	shortJSON          = "-ojson"
	flagVersion        = "--version"
	errVersion2Req     = "version 2 required"
	configValidateLine = "config validate <file>"
)

func noEnv(string) (string, bool) { return "", false }

func TestCmdExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantOut string
		args    []string
	}{
		{
			name:    "lint command should exist",
			args:    []string{cmdLint, "../linter/testdata/valid.yaml", flagFormat, formatYAML},
			wantOut: "  INFO    Valid Semaphore configuration\n",
		},
		{
			name: "evaluate command should exist",
			args: []string{
				cmdEvaluate, flagConfig, "../evaluate/testdata/flag.goff.yaml",
				"--ctx", `{"targetingKey": "user-123"}`, flagFormat, formatYAML,
			},
			wantOut: `{
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

			var stdout, stderr bytes.Buffer

			code := cliapp.Main(t.Context(), tt.args, &stdout, &stderr, noEnv, cliapp.Build{Version: localDevVersion})
			assert.Equal(t, 0, code, stderr.String())
			assert.Equal(t, tt.wantOut, pterm.RemoveColorFromString(stdout.String()))
		})
	}
}

func TestVersionFlag(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{flagVersion, "-v"} {
		t.Run(flag, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := cliapp.Main(
				t.Context(),
				[]string{flag},
				&stdout,
				&stderr,
				noEnv,
				cliapp.Build{Version: localDevVersion},
			)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr.String())
			assert.Contains(t, stdout.String(), "semaphore-cli localdev\ncommit: ")
			assert.Contains(t, stdout.String(), "\nfips140: false\n")
		})
	}
}

func TestEmptyArgsPrintsRootHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer

	code := cliapp.Main(t.Context(), nil, &stdout, &stderr, noEnv, cliapp.Build{Version: localDevVersion})
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr.String())
	assert.Contains(t, stdout.String(), "Usage: semaphore-cli <command> [flags]")
}

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
			args:    []string{cmdConfig, cmdPrint, flagEffective, file, "-o", fmtJSON},
			wantOut: `"bucket": "b"`,
		},
		{
			name:    "config with no subcommand prints help",
			args:    []string{cmdConfig},
			wantOut: configValidateLine,
		},
		{
			name:    "config with -o json and no subcommand prints help",
			args:    []string{cmdConfig, "-o", fmtJSON},
			wantOut: configValidateLine,
		},
		{
			name:    "config -h prints help",
			args:    []string{cmdConfig, "-h"},
			wantOut: configValidateLine,
		},
		{
			name:    "publish verify exists",
			args:    []string{cmdPublish, cmdVerify, helpFlag},
			wantOut: "Reads the manifest and every listed object",
		},
		{name: "publish exists", args: []string{cmdPublish, helpFlag}, wantOut: "--dry-run"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := cliapp.Main(t.Context(), tt.args, &stdout, &stderr, noEnv, cliapp.Build{Version: localDevVersion})
			assert.Equal(t, 0, code, stderr.String())
			assert.Empty(t, stderr.String())
			assert.Contains(t, pterm.RemoveColorFromString(stdout.String()), tt.wantOut)
		})
	}
}

// TestHelperProcess is not a test: it is the entry point of the process that runCLIClean starts.
//
//nolint:paralleltest // the helper process runs one command line and exits
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("only runs as the helper process of runCLIClean")
	}

	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]

			break
		}
	}

	code := cliapp.Main(
		t.Context(),
		args,
		os.Stdout,
		os.Stderr,
		os.LookupEnv,
		cliapp.Build{Version: localDevVersion},
	)

	os.Exit(code) //nolint:revive // the helper process ends with the exit code of the command line
}

// runCLIClean runs the CLI in a subprocess with an explicit minimal environment so that host variables (including
// COLUMNS or AWS configuration) never leak into test assertions.
func runCLIClean(t *testing.T, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()

	cmd := exec.CommandContext(
		t.Context(),
		os.Args[0],
		append([]string{"-test.run=^TestHelperProcess$", "--"}, args...)...,
	)

	const baseEnvCount = 8

	env := make([]string, 0, baseEnvCount+len(extraEnv))
	env = append(env,
		helperEnv+"=1",
		"NO_COLOR=1",
		"GOCOVERDIR="+t.TempDir(),
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_CONFIG_FILE="+os.DevNull,
		"AWS_SHARED_CREDENTIALS_FILE="+os.DevNull,
	)
	env = append(env, extraEnv...)
	cmd.Env = env

	var stdout, stderr bytes.Buffer

	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	err := cmd.Run()

	code := 0
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}

	return pterm.RemoveColorFromString(stdout.String()), pterm.RemoveColorFromString(stderr.String()), code
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

	// A flag file whose values and query are secrets: the engine rejects it, and no output repeats them.
	const secretQuery, secretValue = "Qy7Wv`Mk3'Zx", "Vr4NmK8dQ"

	secretDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(secretDir, "flags"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(secretDir, "flags", "f.yaml"), []byte(
		"f:\n  variations: {on: true, off: false}\n  defaultRule: {variation: off}\n  metadata: {owner: "+secretValue+"}\n"+
			"  targeting:\n    - query: \""+secretQuery+" eq (\"\n      variation: on\n",
	), 0o600))
	srv := verifyServer(t)
	target := []string{"--bucket", "bucket", "--region", "us-east-1", "--endpoint", srv.URL}

	// One rule for every command: stdout carries results only (and in JSON mode exactly one JSON value), stderr
	// carries every diagnostic, including the final error line. An empty want is checked as exactly empty.
	tests := []struct {
		name       string
		wantStdout string
		wantStderr string
		jsonStdout string   // the "error.code" of the one JSON error value, or "report" for a report
		wantNone   []string // secrets: no fragment of 6 characters of any appears on stdout or stderr
		args       []string
		wantExit   int
	}{
		{name: "version", args: []string{flagVersion}, wantStdout: "semaphore-cli"},
		{name: "unknown root flag", args: []string{unknownFlag}, wantStderr: usageText, wantExit: 1},
		{name: "unknown subcommand", args: []string{"bogus"}, wantStderr: "unexpected argument bogus", wantExit: 1},
		{
			name:       "lint failure",
			args:       []string{cmdLint, missing, flagFormat, formatYAML},
			wantStderr: "impossible to find",
			wantExit:   1,
		},
		{
			name:       "evaluate failure",
			args:       []string{cmdEvaluate, "--path", missing, "--ctx", "{}", flagFormat, formatYAML},
			wantStderr: "no such file",
			wantExit:   1,
		},
		{
			name:       "config failure, argument count",
			args:       []string{cmdConfig, cmdValidate},
			wantStderr: `expected "<file>"`,
			wantExit:   1,
		},
		{
			name:       "config failure, invalid file",
			args:       []string{cmdConfig, cmdValidate, v1File},
			wantStderr: errVersion2Req,
			wantExit:   2,
		},
		{
			name: "publish unknown flag after -o json", args: []string{cmdPublish, unknownFlag, "-o", fmtJSON},
			wantStderr: unknownFlagMsg, jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name: "publish unknown flag before --output=json", args: []string{cmdPublish, unknownFlag, optOutputJSON},
			wantStderr: unknownFlagMsg, jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name: "config unknown flag before -ojson", args: []string{cmdConfig, cmdValidate, unknownFlag, shortJSON},
			wantStderr: unknownFlagMsg, jsonStdout: codeUsage, wantExit: 1,
		},
		{
			name:       "publish unknown flag with -o text last wins",
			args:       []string{cmdPublish, unknownFlag, "-o", fmtJSON, "-o", fmtText},
			wantStderr: unknownFlagMsg,
			wantExit:   1,
		},
		{
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
			name:       "config text parsed, then a bad flag, then json",
			args:       []string{cmdConfig, cmdValidate, "-o", fmtText, unknownFlag, "-o", fmtJSON},
			wantStderr: unknownFlagMsg,
			jsonStdout: codeUsage,
			wantExit:   1,
		},
		{
			name:       "config -o json before subcommand and bad flag after",
			args:       []string{cmdConfig, "-o", fmtJSON, cmdValidate, unknownFlag},
			wantStderr: unknownFlagMsg,
			jsonStdout: codeUsage,
			wantExit:   1,
		},
		{
			name:       "publish: a rejected flag file is not repeated, text",
			args:       append([]string{cmdPublish, secretDir}, target...),
			wantStderr: "invalid query",
			wantExit:   2,
			wantNone:   []string{secretQuery, secretValue},
		},
		{
			name:       "publish: a rejected flag file is not repeated, JSON",
			args:       append([]string{cmdPublish, secretDir, optOutput, fmtJSON}, target...),
			wantStderr: "invalid query",
			jsonStdout: codeInvalid,
			wantExit:   2,
			wantNone:   []string{secretQuery, secretValue},
		},
		{
			name:       "publish: a malformed flag before --output=json, the usage error is JSON",
			args:       []string{cmdPublish, ".", "---dry-run", optOutputJSON},
			wantStderr: "unknown flag ---dry-run",
			jsonStdout: codeUsage,
			wantExit:   1,
		},
		{
			name:       "publish: a malformed flag before --output=text, nothing on stdout",
			args:       []string{cmdPublish, ".", "---dry-run", "--output=text"},
			wantStderr: "unknown flag ---dry-run",
			wantExit:   1,
		},
		{
			name:       "publish: -otext is the value of --config, the output stays json",
			args:       []string{cmdPublish, optOutput, fmtJSON, "--config=-otext", unknownFlag},
			wantStderr: unknownFlagMsg,
			jsonStdout: codeUsage,
			wantExit:   1,
		},
		{
			name:       "publish: -ojson is the value of --config, the output stays text",
			args:       []string{cmdPublish, optOutput, fmtText, "--config=-ojson", unknownFlag},
			wantStderr: unknownFlagMsg,
			wantExit:   1,
		},
		{
			name:       "config: missing --output value before --output=json",
			args:       []string{cmdConfig, cmdValidate, "bootstrap.yaml", optOutput, optOutputJSON},
			wantStderr: "expected value",
			jsonStdout: codeUsage,
			wantExit:   1,
		},
		{
			name: "config failure, JSON", args: []string{cmdConfig, cmdPrint, flagEffective, v1File, "-o", fmtJSON},
			wantStderr: errVersion2Req, jsonStdout: codeInvalid, wantExit: 2,
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
			wantStderr: `expected "<dir>"`, jsonStdout: codeUsage, wantExit: 1,
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

			stdout, stderr, code := runCLIClean(t, nil, tt.args...)

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

			for _, secret := range tt.wantNone {
				for i := 0; i+6 <= len(secret); i++ {
					assert.NotContains(t, stdout, secret[i:i+6], "stdout repeats a fragment of %q", secret)
					assert.NotContains(t, stderr, secret[i:i+6], "stderr repeats a fragment of %q", secret)
				}
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

func TestMainInjectedStreamsAndErrors(t *testing.T) {
	t.Parallel()

	v1File := filepath.Join(t.TempDir(), "v1.yaml")
	require.NoError(t, os.WriteFile(v1File, []byte("port: 1031\n"), 0o600))

	tests := []struct {
		name       string
		wantStdout string
		wantStderr string
		jsonCode   string
		args       []string
		wantExit   int
	}{
		{name: "unknown root flag", args: []string{unknownFlag}, wantStderr: usageText, wantExit: 1},
		{name: "unknown subcommand", args: []string{"bogus"}, wantStderr: "unexpected argument bogus", wantExit: 1},
		{
			name:       "config validate missing file",
			args:       []string{cmdConfig, cmdValidate},
			wantStderr: `expected "<file>"`,
			wantExit:   1,
		},
		{
			name:       "config validate unknown flag json",
			args:       []string{cmdConfig, cmdValidate, unknownFlag, shortJSON},
			wantStderr: unknownFlagMsg,
			jsonCode:   codeUsage,
			wantExit:   1,
		},
		{
			name:       "config validate v1 file",
			args:       []string{cmdConfig, cmdValidate, v1File},
			wantStderr: errVersion2Req,
			wantExit:   2,
		},
		{
			name:       "config validate v1 file with --help=false still validates",
			args:       []string{cmdConfig, cmdValidate, v1File, "--help=false"},
			wantStderr: errVersion2Req,
			wantExit:   2,
		},
		{
			name:       "config validate v1 file with --version=false still validates",
			args:       []string{cmdConfig, cmdValidate, v1File, "--version=false"},
			wantStderr: errVersion2Req,
			wantExit:   2,
		},
		{
			name:       "config validate v1 file with -o=json writes JSON report",
			args:       []string{cmdConfig, cmdValidate, v1File, "-o=json"},
			wantStdout: `"valid": false`,
			wantStderr: errVersion2Req,
			wantExit:   2,
		},
		{
			name:       "config print v1 file with -o=json writes JSON error",
			args:       []string{cmdConfig, "print", "--effective", v1File, "-o=json"},
			wantStderr: errVersion2Req,
			jsonCode:   codeInvalid,
			wantExit:   2,
		},
		{
			name:       "generate no subcommand",
			args:       []string{cmdGenerate},
			wantStderr: `expected "manifest"`,
			wantExit:   1,
		},
		{
			name:       "publish missing dir json",
			args:       []string{cmdPublish, "-o", fmtJSON},
			wantStderr: `expected "<dir>"`,
			jsonCode:   codeUsage,
			wantExit:   1,
		},
		{
			name:       "publish unknown flag json",
			args:       []string{cmdPublish, "-o", fmtJSON, unknownFlag},
			wantStderr: unknownFlagMsg,
			jsonCode:   codeUsage,
			wantExit:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			code := cliapp.Main(
				t.Context(),
				tt.args,
				&stdout,
				&stderr,
				noEnv,
				cliapp.Build{Version: localDevVersion},
			)
			cleanOut := pterm.RemoveColorFromString(stdout.String())
			cleanErr := pterm.RemoveColorFromString(stderr.String())

			assert.Equal(t, tt.wantExit, code)
			assert.Contains(t, cleanErr, tt.wantStderr)

			switch {
			case tt.jsonCode != "":
				assertOneJSON(t, cleanOut, tt.jsonCode)
			case tt.wantStdout != "":
				assert.Contains(t, cleanOut, tt.wantStdout)
			default:
				assert.Empty(t, cleanOut)
			}
		})
	}
}

type failingWriter struct{}

var errWrite = errors.New("disk full")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestMainWriteFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "version write failure", args: []string{flagVersion}},
		{name: "root help write failure", args: nil},
		{name: "config help write failure", args: []string{cmdConfig}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer

			code := cliapp.Main(
				t.Context(),
				tt.args,
				failingWriter{},
				&stderr,
				noEnv,
				cliapp.Build{Version: localDevVersion},
			)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr.String(), errWrite.Error())
		})
	}
}
