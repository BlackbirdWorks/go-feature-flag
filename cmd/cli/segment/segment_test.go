package segment_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/segment"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

const (
	flagSegments  = "--segments"
	flagSegment   = "--segment"
	cmdImport     = "import"
	flagFrom      = "--from"
	flagDebug     = "--debug"
	flagConfig    = "--config"
	cmdAdd        = "add"
	cmdRemove     = "remove"
	cmdStat       = "stat"
	memberOld     = "old"
	bootstrapFile = "relay.yaml"
	segmentsFile  = "segments.yaml"
	liveKey       = "semaphore:segment:{vip}"
	versionKey    = "vip:version"
	canary        = "CANARY-Id-123"

	bootstrapDoc = `version: 2
connections:
  fast: {type: valkey, addresses: ["127.0.0.1:6380"]}
  cache: {type: redis, addresses: ["127.0.0.1:6379"], keyPrefix: "p:"}
  store: {type: file, path: /tmp/x}
segments:
  resolver:
    maxIdBytes: 40
    default: {normalize: trimLower}
`
	segmentsDoc = `apiVersion: semaphore/v1
kind: Segments
segments:
  - {name: vip, connection: fast, mode: lookup, version: {key: vip:version}}
  - {name: plain, connection: cache, mode: lookup}
  - {name: flags, connection: fast, mode: lookup, layout: keys, key: "f:"}
  - {name: ghost, connection: nowhere, mode: lookup}
  - {name: odd, connection: store, mode: lookup}
  - {name: fixed, members: [a]}
`
)

var errConnect = errors.New("connect refused")

type outcome struct {
	err    error
	stdout string
	stderr string
}

func (o *outcome) all() string { return o.stdout + o.stderr + errText(o.err) }

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

func runCmd(t *testing.T, store *admintest.Fake, stdin string, files map[string]string, args ...string) *outcome {
	t.Helper()

	var out, errOut bytes.Buffer

	deps := segment.Deps{
		LookupEnv: func(string) (string, bool) { return "", false },
		ReadFile: func(name string) ([]byte, error) {
			if body, ok := files[name]; ok {
				return []byte(body), nil
			}

			return nil, io.ErrUnexpectedEOF
		},
		Stdin: strings.NewReader(stdin),
		Open: func(name string) (io.ReadCloser, error) {
			if body, ok := files[name]; ok {
				return io.NopCloser(strings.NewReader(body)), nil
			}

			return nil, io.ErrUnexpectedEOF
		},
		Connect: func(context.Context, *bootstrap.Connection, bootstrap.LoadOptions) (segmentadmin.Store, error) {
			if store == nil {
				return nil, errConnect
			}

			return store, nil
		},
	}

	err := segment.Run(t.Context(), args, &helper.Streams{Stdout: &out, Stderr: &errOut}, deps)

	return &outcome{stdout: out.String(), stderr: errOut.String(), err: err}
}

func files() map[string]string {
	return map[string]string{bootstrapFile: bootstrapDoc, segmentsFile: segmentsDoc}
}

func common(name string) []string {
	return []string{flagConfig, bootstrapFile, flagSegments, segmentsFile, flagSegment, name}
}

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr      error
		extra        map[string]string
		failOp       string
		stdin        string
		name         string
		wantNoOutput string
		preload      []string
		wantOut      []string
		args         []string
		wantMembers  []string
		wantCode     int
		noStore      bool
		wantVersion  bool
	}{
		{
			name: "import from a file", args: append([]string{cmdImport, flagFrom, "ids.txt"}, common("vip")...),
			extra: map[string]string{"ids.txt": " A\nb\nB\n"}, preload: []string{memberOld},
			wantOut:     []string{"read: 3", "accepted: 3", "added: 2", "duplicates: 1", "swapped: true", "version: "},
			wantMembers: []string{"a", "b"}, wantVersion: true,
		},
		{
			name: "import from standard input", args: append([]string{cmdImport, flagFrom, "-"}, common("vip")...),
			stdin: "x\n", wantOut: []string{"added: 1"}, wantMembers: []string{"x"}, wantVersion: true,
		},
		{
			name:  "import csv with rejected lines",
			args:  append([]string{cmdImport, flagFrom, "-", "--format", "csv", "--column", "2"}, common("vip")...),
			stdin: "1,a\n2\n3,\n4," + strings.Repeat("z", 50) + "\n",
			wantOut: []string{
				"rejected empty: 1",
				"rejected tooLong: 1",
				"rejected column: 1",
			},
			wantMembers: []string{"a"},
			wantVersion: true,
		},
		{
			name:  "dry run",
			args:  append([]string{cmdImport, flagFrom, "-", "--dry-run"}, common("vip")...),
			stdin: "a\n",
			preload: []string{
				memberOld,
			},
			wantOut:     []string{"dry run: nothing written", "read: 1"},
			wantMembers: []string{memberOld},
		},
		{
			name: "empty input is refused with the invalid exit code",
			args: append(
				[]string{cmdImport, flagFrom, "-"},
				common("vip")...),
			stdin:       "\n",
			preload:     []string{memberOld},
			wantErr:     segmentadmin.ErrEmptyInput,
			wantCode:    helper.ExitInvalid,
			wantMembers: []string{memberOld},
		},
		{
			name: "allow empty", args: append([]string{cmdImport, flagFrom, "-", "--allow-empty"}, common("vip")...),
			preload: []string{memberOld}, wantOut: []string{"swapped: true"}, wantVersion: true,
		},
		{
			name: "keys layout import is refused", args: append([]string{cmdImport, flagFrom, "-"}, common("flags")...),
			stdin: "a\n", wantErr: segmentadmin.ErrKeysLayout, wantCode: helper.ExitInvalid,
		},
		{
			name: "a store failure has the transport exit code", failOp: "SADD",
			args: append([]string{cmdImport, flagFrom, "-"}, common("vip")...), stdin: "a\n",
			wantErr: segmentadmin.ErrStore, wantCode: helper.ExitTransport,
		},
		{
			name: "missing input file", args: append([]string{cmdImport, flagFrom, "nope.txt"}, common("vip")...),
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name: cmdAdd, args: append([]string{cmdAdd, "X", " y "}, common("vip")...), preload: []string{"a"},
			wantOut: []string{"added: 2", "version: "}, wantMembers: []string{"a", "x", "y"}, wantVersion: true,
		},
		{
			name:        "add from standard input",
			args:        append([]string{cmdAdd, flagFrom, "-"}, common("vip")...),
			stdin:       "q\n",
			wantOut:     []string{"added: 1"},
			wantMembers: []string{"q"},
			wantVersion: true,
		},
		{
			name: cmdRemove, args: append([]string{cmdRemove, "A"}, common("vip")...), preload: []string{"a", "b"},
			wantOut: []string{"removed: 1"}, wantMembers: []string{"b"}, wantVersion: true,
		},
		{
			name: "add without an id", args: append([]string{cmdAdd}, common("vip")...),
			wantErr: segment.ErrNoInput, wantCode: helper.ExitFailure,
		},
		{
			name: cmdStat, args: append([]string{cmdStat}, common("vip")...), preload: []string{"a", "b"},
			wantOut: []string{"exists: true", "members: 2", "version: none"}, wantMembers: []string{"a", "b"},
		},
		{
			name: "stat of a keys layout", args: append([]string{cmdStat, "--scan-limit", "5"}, common("flags")...),
			wantOut: []string{"exists: false", "members: 0"},
		},
		{
			name: "stat failure", failOp: "PTTL", args: append([]string{cmdStat}, common("vip")...),
			wantErr: admintest.ErrInjected,
		},
		{
			name: "default key of a connection with a prefix", args: append([]string{cmdStat}, common("plain")...),
			wantOut: []string{"exists: false"},
		},
		{name: "unknown segment", args: append([]string{cmdStat}, common("none")...), wantErr: segment.ErrNoSegment},
		{name: "static segment", args: append([]string{cmdStat}, common("fixed")...), wantErr: segment.ErrStatic},
		{
			name:    "unknown connection",
			args:    append([]string{cmdStat}, common("ghost")...),
			wantErr: segment.ErrNoConnection,
		},
		{
			name:    "not a store connection",
			args:    append([]string{cmdStat}, common("odd")...),
			wantErr: segment.ErrNotStore,
		},
		{
			name:    "connect failure",
			args:    append([]string{cmdStat}, common("vip")...),
			noStore: true, wantErr: errConnect,
		},
		{
			name: "ping failure", failOp: "PING", args: append([]string{cmdStat}, common("vip")...),
			wantErr: admintest.ErrInjected,
		},
		{
			name:    "missing segments document",
			args:    []string{cmdStat, flagConfig, bootstrapFile, flagSegments, "gone.yaml", flagSegment, "vip"},
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "missing bootstrap file",
			args:    []string{cmdStat, flagConfig, "gone.yaml", flagSegments, segmentsFile, flagSegment, "vip"},
			wantErr: io.ErrUnexpectedEOF,
		},
		{name: "usage error", args: []string{cmdStat}, wantErr: helper.ErrUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			if len(tt.preload) > 0 {
				_, err := store.SAdd(t.Context(), liveKeyOf(tt.args), tt.preload, 0)
				require.NoError(t, err)
			}

			if tt.failOp != "" {
				store.Fail(tt.failOp, 1)
			}

			docs := files()
			maps.Copy(docs, tt.extra)

			var used *admintest.Fake
			if !tt.noStore {
				used = store
			}

			got := runCmd(t, used, tt.stdin, docs, tt.args...)

			if tt.wantErr != nil {
				require.ErrorIs(t, got.err, tt.wantErr)

				if tt.wantCode != 0 {
					assert.Equal(t, tt.wantCode, helper.ExitCodeOf(got.err))
				}
			} else {
				require.NoError(t, got.err)
			}

			for _, want := range tt.wantOut {
				assert.Contains(t, got.stdout, want)
			}

			if tt.wantMembers != nil {
				assert.Equal(t, tt.wantMembers, store.Members(liveKeyOf(tt.args)))
			}

			_, versioned, _ := store.Value(t.Context(), versionKey)
			assert.Equal(t, tt.wantVersion, versioned)
		})
	}
}

// liveKeyOf is the key of the segment that the arguments name.
func liveKeyOf(args []string) string {
	for i, arg := range args {
		if arg == flagSegment && args[i+1] == "plain" {
			return "p:{plain}"
		}
	}

	return liveKey
}

func TestRun_noMemberIdInOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		failOp string
		stdin  string
		args   []string
	}{
		{
			name:  cmdImport,
			args:  append([]string{cmdImport, flagFrom, "-", flagDebug}, common("vip")...),
			stdin: canary + "\n",
		},
		{
			name: "import with a failed batch", failOp: "SADD", stdin: canary + "\n",
			args: append([]string{cmdImport, flagFrom, "-", flagDebug}, common("vip")...),
		},
		{
			name: "import of rejected lines", stdin: canary + canary + canary + "\n",
			args: append([]string{cmdImport, flagFrom, "-", flagDebug, "--allow-empty"}, common("vip")...),
		},
		{
			name:  "import of a csv",
			stdin: "1," + canary + "\n",
			args: append(
				[]string{cmdImport, flagFrom, "-", flagDebug, "--format", "csv", "--column", "2"},
				common("vip")...),
		},
		{name: cmdAdd, args: append([]string{cmdAdd, canary, flagDebug}, common("vip")...)},
		{
			name:   "add with a failed bump",
			failOp: "SET",
			args:   append([]string{cmdAdd, canary, flagDebug}, common("vip")...),
		},
		{name: cmdRemove, args: append([]string{cmdRemove, canary, flagDebug}, common("vip")...)},
		{name: cmdStat, args: append([]string{cmdStat, flagDebug}, common("vip")...)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			_, err := store.SAdd(t.Context(), liveKey, []string{canary}, 0)
			require.NoError(t, err)

			if tt.failOp != "" {
				store.Fail(tt.failOp, 1)
			}

			got := runCmd(t, store, tt.stdin, files(), tt.args...)

			assert.NotContains(t, strings.ToLower(got.all()), strings.ToLower(canary))

			for _, call := range store.Calls() {
				assert.NotContains(t, strings.ToLower(call), strings.ToLower(canary))
			}
		})
	}
}

func TestDefaultDeps(t *testing.T) {
	t.Parallel()

	deps := segment.DefaultDeps()
	assert.NotNil(t, deps.ReadFile)
	assert.NotNil(t, deps.Stdin)

	_, err := deps.Open("/nonexistent/path/to/ids")
	assert.Error(t, err)
}

func TestConnect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		doc     string
	}{
		{name: "valkey", doc: `version: 2
connections:
  c: {type: valkey, addresses: ["127.0.0.1:1"], forceSingleClient: true}
`},
		{name: "redis", doc: `version: 2
connections:
  c: {type: redis, addresses: ["127.0.0.1:1"]}
`},
		{name: "file", doc: `version: 2
connections:
  c: {type: file, path: /tmp/x}
`, wantErr: segment.ErrNotStore},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tt.doc), nil, bootstrap.LoadOptions{})
			require.NoError(t, err)

			conn := cfg.Connections["c"]

			store, err := segment.DefaultDeps().Connect(t.Context(), &conn, bootstrap.LoadOptions{})
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)

				return
			}

			if err != nil {
				return // valkey-go refuses a client when nothing answers: the error names no address
			}

			assert.NotNil(t, store)
			store.Close()
		})
	}
}

func TestHelpVars(t *testing.T) {
	t.Parallel()

	assert.Contains(t, segment.HelpVars(), "helpSegment")
}
