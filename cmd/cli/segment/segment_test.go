package segment_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/segment"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

const (
	wantVersionLine = "version: "
	wantAdded2      = "added: 2"
	segVIP          = "vip"
	cmdBump         = "bump"
	opSET           = "SET"
	wantAdded1      = "added: 1"
	flagSegments    = "--segments"
	flagSegment     = "--segment"
	cmdImport       = "import"
	flagFrom        = "--from"
	flagDebug       = "--debug"
	flagConfig      = "--config"
	cmdAdd          = "add"
	cmdRemove       = "remove"
	cmdStat         = "stat"
	memberOld       = "old"
	bootstrapFile   = "relay.yaml"
	segmentsFile    = "segments.yaml"
	liveKey         = "semaphore:segment:{vip}"
	versionKey      = "vip:version"
	canary          = "CANARY-Id-123"

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
	bareDoc = `version: 2
connections:
  fast: {type: valkey, addresses: ["127.0.0.1:6380"]}
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

	err := runWith(
		t.Context(),
		store,
		strings.NewReader(stdin),
		files,
		&helper.Streams{Stdout: &out, Stderr: &errOut},
		args,
	)

	return &outcome{stdout: out.String(), stderr: errOut.String(), err: err}
}

func runWith(
	ctx context.Context, store *admintest.Fake, stdin io.Reader, files map[string]string, streams *helper.Streams,
	args []string,
) error {
	deps := segment.Deps{
		LookupEnv: func(string) (string, bool) { return "", false },
		ReadFile: func(name string) ([]byte, error) {
			if body, ok := files[name]; ok {
				return []byte(body), nil
			}

			return nil, io.ErrUnexpectedEOF
		},
		Stdin: stdin,
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

	return segment.Run(ctx, args, streams, deps)
}

func files() map[string]string {
	return map[string]string{bootstrapFile: bootstrapDoc, segmentsFile: segmentsDoc, "bare.yaml": bareDoc}
}

func common(name string) []string {
	return []string{flagConfig, bootstrapFile, flagSegments, segmentsFile, flagSegment, name}
}

// runCase is one row of TestRun.
type runCase struct {
	wantErr     error
	extra       map[string]string
	failOp      string
	stdin       string
	name        string
	lockedBy    string
	loseOp      string
	wantMembers []string
	preload     []string
	wantOut     []string
	args        []string
	wantCode    int
	failAll     bool
	noStore     bool
	wantVersion bool
}

func runCases1() []runCase {
	return []runCase{
		{
			name:    "import from a file",
			args:    append([]string{cmdImport, flagFrom, "ids.txt"}, common(segVIP)...),
			extra:   map[string]string{"ids.txt": " A\nb\nB\n"},
			preload: []string{memberOld},
			wantOut: []string{
				"read: 3",
				"accepted: 3",
				wantAdded2,
				"duplicates: 1",
				"swapped: true",
				wantVersionLine,
			},
			wantMembers: []string{"a", "b"},
			wantVersion: true,
		},
		{
			name: "import from standard input", args: append([]string{cmdImport, flagFrom, "-"}, common(segVIP)...),
			stdin: "x\n", wantOut: []string{wantAdded1}, wantMembers: []string{"x"}, wantVersion: true,
		},
		{
			name:  "import csv with rejected lines",
			args:  append([]string{cmdImport, flagFrom, "-", "--format", "csv", "--column", "2"}, common(segVIP)...),
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
			args:  append([]string{cmdImport, flagFrom, "-", "--dry-run"}, common(segVIP)...),
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
				common(segVIP)...),
			stdin:       "\n",
			preload:     []string{memberOld},
			wantErr:     segmentadmin.ErrEmptyInput,
			wantCode:    helper.ExitInvalid,
			wantMembers: []string{memberOld},
		},
		{
			name: "allow empty", args: append([]string{cmdImport, flagFrom, "-", "--allow-empty"}, common(segVIP)...),
			preload: []string{memberOld}, wantOut: []string{"swapped: true"}, wantVersion: true,
		},
		{
			name: "keys layout import is refused", args: append([]string{cmdImport, flagFrom, "-"}, common("flags")...),
			stdin: "a\n", wantErr: segmentadmin.ErrKeysLayout, wantCode: helper.ExitInvalid,
		},
		{
			name: "a store failure has the transport exit code", failOp: "SADD", failAll: true,
			args: append([]string{cmdImport, flagFrom, "-"}, common(segVIP)...), stdin: "a\n",
			wantErr: segmentadmin.ErrStore, wantCode: helper.ExitTransport,
		},
		{
			name: "missing input file", args: append([]string{cmdImport, flagFrom, "nope.txt"}, common(segVIP)...),
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name: cmdAdd, args: append([]string{cmdAdd, "X", " y "}, common(segVIP)...), preload: []string{"a"},
			wantOut: []string{wantAdded2, wantVersionLine}, wantMembers: []string{"a", "x", "y"}, wantVersion: true,
		},
		{
			name:        "add from standard input",
			args:        append([]string{cmdAdd, flagFrom, "-"}, common(segVIP)...),
			stdin:       "q\n",
			wantOut:     []string{wantAdded1},
			wantMembers: []string{"q"},
			wantVersion: true,
		},
	}
}

func runCases2() []runCase {
	return []runCase{
		{
			name: cmdRemove, args: append([]string{cmdRemove, "A"}, common(segVIP)...), preload: []string{"a", "b"},
			wantOut: []string{"removed: 1"}, wantMembers: []string{"b"}, wantVersion: true,
		},
		{
			name: "a line bound under the minimum is invalid input",
			args: append([]string{cmdImport, flagFrom, "-", "--max-line-bytes", "4"}, common(segVIP)...), stdin: "a\n",
			wantErr: segmentadmin.ErrBadOptions, wantCode: helper.ExitInvalid,
		},
		{
			name: "an import that finds the lock taken is a conflict", lockedBy: "other", preload: []string{memberOld},
			args: append([]string{cmdImport, flagFrom, "-"}, common(segVIP)...), stdin: "a\n",
			wantErr: segmentadmin.ErrImportRunning, wantCode: helper.ExitConflict, wantMembers: []string{memberOld},
		},
		{
			name:        "an id with a line break is rejected, not split",
			args:        append([]string{cmdAdd, "a\nb", "ok"}, common(segVIP)...),
			wantOut:     []string{wantAdded1, "rejected invalid: 1"},
			wantMembers: []string{"ok"},
			wantVersion: true,
		},
		{
			name: "no segments section: the relay defaults apply, a 513 byte id is rejected",
			args: []string{
				cmdImport,
				flagFrom,
				"-",
				flagConfig,
				"bare.yaml",
				flagSegments,
				segmentsFile,
				flagSegment,
				segVIP,
			},
			stdin:       "ok\n" + strings.Repeat("z", 513) + "\n",
			wantOut:     []string{"rejected tooLong: 1", wantAdded1},
			wantMembers: []string{"ok"},
			wantVersion: true,
		},
		{
			name:        "a byte order mark is not part of the first id",
			args:        append([]string{cmdImport, flagFrom, "-"}, common(segVIP)...),
			stdin:       "\xef\xbb\xbfFirst\nsecond\n",
			wantOut:     []string{wantAdded2},
			wantMembers: []string{"first", "second"},
			wantVersion: true,
		},
		{
			name: "no answer from the swap exits with the unknown code", loseOp: "SWAP",
			args: append([]string{cmdImport, flagFrom, "-"}, common(segVIP)...), stdin: "a\n",
			wantErr: segmentadmin.ErrSwapUnknown, wantCode: helper.ExitUnknown,
		},
		{
			name: "an add while an import runs is a conflict", lockedBy: "other", preload: []string{memberOld},
			args: append([]string{cmdAdd, "a"}, common(segVIP)...), wantErr: segmentadmin.ErrImportRunning,
			wantCode: helper.ExitConflict, wantMembers: []string{memberOld},
		},
		{
			name: cmdBump, args: append([]string{cmdBump}, common(segVIP)...), wantOut: []string{wantVersionLine},
			wantVersion: true,
		},
		{
			name: "bump needs a version key", args: append([]string{cmdBump}, common("plain")...),
			wantErr: segment.ErrNoVersionKey,
		},
		{
			name:    "bump that cannot write says so",
			failOp:  opSET,
			failAll: true,
			args:    append([]string{cmdBump}, common(segVIP)...),
			wantErr: segmentadmin.ErrNotBumped,
		},
	}
}

func runCases3() []runCase {
	return []runCase{
		{
			name: "add without an id", args: append([]string{cmdAdd}, common(segVIP)...),
			wantErr: segment.ErrNoInput, wantCode: helper.ExitFailure,
		},
		{
			name: cmdStat, args: append([]string{cmdStat}, common(segVIP)...), preload: []string{"a", "b"},
			wantOut: []string{"exists: true", "members: 2", "version: none"}, wantMembers: []string{"a", "b"},
		},
		{
			name: "stat of a keys layout", args: append([]string{cmdStat, "--scan-limit", "5"}, common("flags")...),
			wantOut: []string{"exists: false", "members: 0"},
		},
		{
			name: "stat failure", failOp: "PTTL", args: append([]string{cmdStat}, common(segVIP)...),
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
			args:    append([]string{cmdStat}, common(segVIP)...),
			noStore: true, wantErr: errConnect,
		},
		{
			name: "ping failure", failOp: "PING", args: append([]string{cmdStat}, common(segVIP)...),
			wantErr: admintest.ErrInjected,
		},
		{
			name:    "missing segments document",
			args:    []string{cmdStat, flagConfig, bootstrapFile, flagSegments, "gone.yaml", flagSegment, segVIP},
			wantErr: io.ErrUnexpectedEOF,
		},
		{
			name:    "missing bootstrap file",
			args:    []string{cmdStat, flagConfig, "gone.yaml", flagSegments, segmentsFile, flagSegment, segVIP},
			wantErr: io.ErrUnexpectedEOF,
		},
		{name: "usage error", args: []string{cmdStat}, wantErr: helper.ErrUsage},
	}
}

func runCases() []runCase {
	return slices.Concat(runCases1(), runCases2(), runCases3())
}

func TestRun(t *testing.T) {
	t.Parallel()

	for _, tt := range runCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := admintest.New()
			if len(tt.preload) > 0 {
				_, err := store.SAdd(t.Context(), liveKeyOf(tt.args), tt.preload, 0)
				require.NoError(t, err)
			}

			switch {
			case tt.failOp != "" && tt.failAll:
				store.FailFrom(tt.failOp, 1)
			case tt.failOp != "":
				store.Fail(tt.failOp, 1)
			}

			if tt.loseOp != "" {
				store.LoseReply(tt.loseOp, 1, true)
			}

			if tt.lockedBy != "" {
				store.Put(liveKey+":import:lock", tt.lockedBy)
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
			args:  append([]string{cmdImport, flagFrom, "-", flagDebug}, common(segVIP)...),
			stdin: canary + "\n",
		},
		{
			name: "import with a failed batch", failOp: "SADD", stdin: canary + "\n",
			args: append([]string{cmdImport, flagFrom, "-", flagDebug}, common(segVIP)...),
		},
		{
			name: "import of rejected lines", stdin: canary + canary + canary + "\n",
			args: append([]string{cmdImport, flagFrom, "-", flagDebug, "--allow-empty"}, common(segVIP)...),
		},
		{
			name:  "import of a csv",
			stdin: "1," + canary + "\n",
			args: append(
				[]string{cmdImport, flagFrom, "-", flagDebug, "--format", "csv", "--column", "2"},
				common(segVIP)...),
		},
		{name: cmdAdd, args: append([]string{cmdAdd, canary, flagDebug}, common(segVIP)...)},
		{
			name:   "add with a failed bump",
			failOp: "SET",
			args:   append([]string{cmdAdd, canary, flagDebug}, common(segVIP)...),
		},
		{name: cmdRemove, args: append([]string{cmdRemove, canary, flagDebug}, common(segVIP)...)},
		{name: cmdStat, args: append([]string{cmdStat, flagDebug}, common(segVIP)...)},
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

func TestRun_inputThatNeverEnds(t *testing.T) {
	t.Parallel()

	pipeReader, pipeWriter := io.Pipe()

	t.Cleanup(func() { _ = pipeWriter.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	store := admintest.New()
	result := make(chan error, 1)

	go func() {
		var out, errOut bytes.Buffer

		result <- runWith(ctx, store, pipeReader, files(), &helper.Streams{Stdout: &out, Stderr: &errOut},
			append([]string{cmdImport, flagFrom, "-"}, common(segVIP)...))
	}()

	require.Eventually(t, func() bool { return len(store.Calls()) > 0 }, 10*time.Second, time.Millisecond,
		"the import did not start")
	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the command did not stop with its context")
	}

	assert.Empty(t, store.Keys(), "nothing is left in the store")
}

type failingWriter struct{}

var errWrite = errors.New("write failed")

func (failingWriter) Write([]byte) (int, error) { return 0, errWrite }

func TestRun_resultWriteError(t *testing.T) {
	t.Parallel()

	err := runWith(t.Context(), admintest.New(), strings.NewReader("a\n"), files(),
		&helper.Streams{Stdout: failingWriter{}, Stderr: io.Discard},
		append([]string{cmdImport, flagFrom, "-"}, common(segVIP)...))
	require.ErrorIs(t, err, errWrite)
}
