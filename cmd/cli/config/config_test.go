package config_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/config"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

const (
	validName    = "valid.yaml"
	v1Name       = "v1.yaml"
	cmdValidate  = "validate"
	cmdPrint     = "print"
	fmtJSON      = "json"
	optEffective = "--effective"

	canaryAdminKey = "canary-admin-key-3b8e"
	canaryEnvValue = "canary-env-secret-91c4"

	validFile = `version: 2
connections:
  control:
    type: s3
    region: us-east-1
    bucket: acme-control
    credentials:
      type: static
      accessKeyId: {fromEnv: CONTROL_KEY_ID}
      secretAccessKey: {fromFile: /run/secrets/control-secret}
auth:
  adminKeys:
    - ` + canaryAdminKey + `
    - {fromEnv: ADMIN_KEY}
controlPlane:
  connection: control
  manifest:
    key: manifests/stable.yaml
`
)

func pollingFile(setting string) string {
	return "version: 2\nconnections:\n  control: {type: s3, region: us-east-1, bucket: b}\n" +
		"controlPlane:\n  connection: control\n  manifest: {key: m.yaml, " + setting + "}\n"
}

func testFiles() map[string]string {
	return map[string]string{
		validName:      validFile,
		v1Name:         "port: 1031\nretrievers:\n  - kind: file\n",
		"unknown.yaml": "version: 2\nconnections:\n  control:\n    type: s3\n    region: us-east-1\n    bogus: 1\n",
		"syntax.yaml":  "version: 2\nconnections: [unterminated\n",
		"dangling.yaml": `version: 2
connections:
  control: {type: s3, region: us-east-1, bucket: b}
controlPlane:
  connection: missing
  manifest: {key: m.yaml}
`,
		"fastpoll.yaml":  pollingFile("pollInterval: 500ms"),
		"jitterone.yaml": pollingFile("jitter: 1"),
	}
}

func testDeps() config.Deps {
	return config.Deps{
		LookupEnv: func(name string) (string, bool) {
			env := map[string]string{
				"CONTROL_KEY_ID": canaryEnvValue, "ADMIN_KEY": canaryEnvValue, "SEMAPHORE_LOG_LEVEL": "debug",
			}
			v, ok := env[name]

			return v, ok
		},
		ReadFile: func(name string) ([]byte, error) {
			if data, ok := testFiles()[name]; ok {
				return []byte(data), nil
			}

			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		},
	}
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	streams := &helper.Streams{Stdout: &out, Stderr: &errOut}
	err := config.Run(args, streams, testDeps())

	assert.Empty(t, errOut.String(), "the commands print nothing on stderr themselves")

	return out.String(), err
}

func golden(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)

	return string(b)
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantGolden string
		wantErr    string
		args       []string
		wantCode   int
	}{
		{name: "valid, text", args: []string{cmdValidate, validName}, wantGolden: "validate_ok.txt"},
		{name: "valid, json", args: []string{cmdValidate, validName, "-o", fmtJSON}, wantGolden: "validate_ok.json"},
		{
			name: "version 1 file, text", args: []string{cmdValidate, v1Name}, wantCode: helper.ExitInvalid,
			wantErr: "v1.yaml: version (line 1): bootstrap config: version 2 required",
		},
		{
			name:       "version 1 file, json",
			args:       []string{cmdValidate, v1Name, "-o", fmtJSON},
			wantCode:   helper.ExitInvalid,
			wantErr:    "version 2 required",
			wantGolden: "validate_v1.json",
		},
		{
			name:     "unknown key names file, path and line",
			args:     []string{cmdValidate, "unknown.yaml"},
			wantCode: helper.ExitInvalid,
			wantErr:  "unknown.yaml: connections.control.bogus (line 6): unknown key",
		},
		{
			name:     "invalid YAML",
			args:     []string{cmdValidate, "syntax.yaml"},
			wantCode: helper.ExitInvalid,
			wantErr:  "syntax.yaml: ",
		},
		{
			name: "dangling reference", args: []string{cmdValidate, "dangling.yaml"}, wantCode: helper.ExitInvalid,
			wantErr: "dangling.yaml: controlPlane.connection (line 5): unknown connection",
		},
		{
			name: "poll interval the reader refuses", args: []string{cmdValidate, "fastpoll.yaml"},
			wantCode: helper.ExitInvalid,
			wantErr:  "fastpoll.yaml: controlPlane.manifest.pollInterval (line 6): invalid value: expected at least 1s",
		},
		{
			name: "jitter the reader refuses", args: []string{cmdValidate, "jitterone.yaml"},
			wantCode: helper.ExitInvalid,
			wantErr:  "jitterone.yaml: controlPlane.manifest.jitter (line 6): invalid value",
		},
		{
			name:     "missing file",
			args:     []string{cmdValidate, "nope.yaml"},
			wantCode: helper.ExitFailure,
			wantErr:  "nope.yaml",
		},
		{
			name:     "no file",
			args:     []string{cmdValidate},
			wantCode: helper.ExitFailure,
			wantErr:  `expected "<file>"`,
		},
		{
			name:     "bad output",
			args:     []string{cmdValidate, validName, "-o", "yaml"},
			wantCode: helper.ExitFailure,
			wantErr:  "--output must be text or json",
		},
		{
			name:     "unknown flag",
			args:     []string{cmdValidate, "--nope"},
			wantCode: helper.ExitFailure,
			wantErr:  "unknown flag --nope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, tt.args...)

			if tt.wantCode == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, helper.ExitCodeOf(err), err.Error())
				assert.Contains(t, err.Error(), tt.wantErr)
			}

			if tt.wantGolden != "" {
				assert.Equal(t, golden(t, tt.wantGolden), out)
			} else {
				assert.Empty(t, out, "text output of a failure is the error alone")
			}

			assert.NotContains(t, out+errString(err), canaryAdminKey)
			assert.NotContains(t, out+errString(err), canaryEnvValue)
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func TestPrintEffective(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantGolden string
		wantErr    string
		args       []string
		wantCode   int
	}{
		{name: "text", args: []string{cmdPrint, optEffective, validName}, wantGolden: "print_effective.yaml"},
		{
			name:       fmtJSON,
			args:       []string{cmdPrint, optEffective, validName, "-o", fmtJSON},
			wantGolden: "print_effective.json",
		},
		{
			name:     "needs --effective",
			args:     []string{cmdPrint, validName},
			wantCode: helper.ExitFailure,
			wantErr:  "--effective is required",
		},
		{
			name:     "invalid file",
			args:     []string{cmdPrint, optEffective, v1Name},
			wantCode: helper.ExitInvalid,
			wantErr:  "version 2 required",
		},
		{
			name:     "bad output",
			args:     []string{cmdPrint, optEffective, validName, "-o", "xml"},
			wantCode: helper.ExitFailure,
			wantErr:  "--output must be text or json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, tt.args...)

			if tt.wantCode == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, helper.ExitCodeOf(err))
				assert.Contains(t, err.Error(), tt.wantErr)
			}

			if tt.wantGolden != "" {
				assert.Equal(t, golden(t, tt.wantGolden), out)
			}

			assert.NotContains(t, out, canaryAdminKey, "admin keys are redacted")
			assert.NotContains(t, out, canaryEnvValue, "secret references are printed as written, never resolved")
		})
	}
}

func TestPrintEffectiveJSONIsOneDocument(t *testing.T) {
	t.Parallel()

	out, err := run(t, cmdPrint, optEffective, validName, "-o", fmtJSON)
	require.NoError(t, err)

	var doc map[string]any

	require.NoError(t, json.Unmarshal([]byte(out), &doc))
	assert.InDelta(t, 2, doc["version"], 0)
}

func TestConfigWithoutSubcommandShowsHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "help flag", args: []string{"--help"}},
		{name: "output flag only", args: []string{"-o", fmtJSON}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, tt.args...)
			require.NoError(t, err)
			assert.Contains(t, out, cmdValidate)
			assert.Contains(t, out, cmdPrint)
		})
	}
}

func TestParseErrorsInJSONMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantCode string
		args     []string
	}{
		{
			name:     "unknown flag with -o json",
			args:     []string{cmdValidate, "--nope", "-o", fmtJSON},
			wantCode: "usage",
		},
		{
			name:     "missing arg with --output=json",
			args:     []string{cmdValidate, "--output=json"},
			wantCode: "usage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, tt.args...)
			require.Error(t, err)
			assert.Equal(t, helper.ExitFailure, helper.ExitCodeOf(err))

			var doc struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &doc))
			assert.Equal(t, tt.wantCode, doc.Error.Code)
		})
	}
}

var errWrite = errors.New("disk full")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestWriteFailuresAreReported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "validate, text", args: []string{cmdValidate, validName}},
		{name: "validate, json", args: []string{cmdValidate, validName, "-o", fmtJSON}},
		{name: "validate of an invalid file, json", args: []string{cmdValidate, v1Name, "-o", fmtJSON}},
		{name: "print, text", args: []string{cmdPrint, optEffective, validName}},
		{name: "print, json", args: []string{cmdPrint, optEffective, validName, "-o", fmtJSON}},
		{name: "print of an invalid file, json", args: []string{cmdPrint, optEffective, v1Name, "-o", fmtJSON}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			streams := &helper.Streams{Stdout: failingWriter{}, Stderr: failingWriter{}}
			err := config.Run(tt.args, streams, testDeps())

			require.ErrorContains(t, err, errWrite.Error())
		})
	}
}

func TestDefaultDepsIsWired(t *testing.T) {
	t.Parallel()

	deps := config.DefaultDeps()
	assert.NotNil(t, deps.LookupEnv)
	assert.NotNil(t, deps.ReadFile)
}
