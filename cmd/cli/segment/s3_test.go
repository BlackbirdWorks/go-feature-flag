package segment_test

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/segment"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

const (
	s3Bootstrap = `version: 2
connections:
  fast: {type: valkey, addresses: ["127.0.0.1:6380"]}
  src: {type: s3, region: us-east-1, bucket: bkt}
`
	cmdSync     = "sync"
	flagOnce    = "--once"
	flagSrcCon  = "--source-connection"
	urlIDs      = "s3://bkt/ids/list.txt"
	urlPrefix   = "s3://bkt/ids/"
	keyList     = "ids/list.txt"
	fileS3      = "s3.yaml"
	wantSwapped = "swapped: true"
	fileIDs     = "ids.txt"
	sourceKey   = liveKey + ":import:source"
)

// fakeS3 is an in-memory bucket behind the S3 client interface of the source.
type fakeS3 struct {
	objs  map[string][]byte
	gzip  map[string]bool
	calls []string
	mu    sync.Mutex
}

func newFakeS3() *fakeS3 { return &fakeS3{objs: map[string][]byte{}, gzip: map[string]bool{}} }

func (f *fakeS3) HeadObject(
	_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "HEAD "+*input.Key)

	if _, ok := f.objs[*input.Key]; !ok {
		return nil, &types.NotFound{}
	}

	return &s3.HeadObjectOutput{
		ETag:          etagPtr(f.objs[*input.Key]),
		ContentLength: aws.Int64(int64(len(f.objs[*input.Key]))),
	}, nil
}

func (f *fakeS3) GetObject(
	_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "GET "+*input.Key)

	data, ok := f.objs[*input.Key]

	switch {
	case !ok:
		return nil, &types.NoSuchKey{}
	case input.IfMatch != nil && *input.IfMatch != *etagPtr(f.objs[*input.Key]):
		return nil, &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "changed"}
	}

	out := &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data)), ContentLength: aws.Int64(int64(len(data)))}
	if f.gzip[*input.Key] {
		out.ContentEncoding = aws.String("gzip")
	}

	return out, nil
}

func (f *fakeS3) ListObjectsV2(
	_ context.Context, input *s3.ListObjectsV2Input, _ ...func(*s3.Options),
) (*s3.ListObjectsV2Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "LIST "+*input.Prefix)

	after := aws.ToString(input.ContinuationToken)
	out := &s3.ListObjectsV2Output{}

	for _, key := range slices.Sorted(maps.Keys(f.objs)) {
		if !strings.HasPrefix(key, *input.Prefix) || key <= after {
			continue
		}

		if len(out.Contents) == 2 {
			out.IsTruncated = aws.Bool(true)
			out.NextContinuationToken = out.Contents[1].Key

			break
		}

		out.Contents = append(out.Contents, types.Object{
			Key: aws.String(key), ETag: etagPtr(f.objs[key]), Size: aws.Int64(int64(len(f.objs[key]))),
		})
	}

	return out, nil
}

func etagPtr(data []byte) *string { return aws.String(`"` + string(data) + `"`) }

func (f *fakeS3) put(key, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.objs[key] = []byte(data)
}

func s3Files() map[string]string {
	all := files()
	all[fileS3] = s3Bootstrap
	all["none.yaml"] = bareDoc
	all["two.yaml"] = s3Bootstrap + "  other: {type: s3, region: us-east-1, bucket: other}\n"

	return all
}

func runS3(
	t *testing.T, store *admintest.Fake, client segment.S3Client, all map[string]string, args ...string,
) *outcome {
	t.Helper()

	var out, errOut bytes.Buffer

	deps := segment.Deps{
		LookupEnv: func(string) (string, bool) { return "", false },
		ReadFile: func(name string) ([]byte, error) {
			if body, ok := all[name]; ok {
				return []byte(body), nil
			}

			return nil, io.ErrUnexpectedEOF
		},
		Connect: func(context.Context, *bootstrap.Connection, bootstrap.LoadOptions) (segmentadmin.Store, error) {
			return store, nil
		},
		NewS3: func(context.Context, *connection.S3Spec, string) (segment.S3Client, error) { return client, nil },
	}

	err := segment.Run(t.Context(), args, &helper.Streams{Stdout: &out, Stderr: &errOut}, &deps)

	return &outcome{stdout: out.String(), stderr: errOut.String(), err: err}
}

// runS3Real runs a command with the real connection factory (nil NewS3).
func runS3Real(t *testing.T, store *admintest.Fake, all map[string]string, args ...string) *outcome {
	t.Helper()

	var out, errOut bytes.Buffer

	deps := segment.Deps{
		LookupEnv: func(name string) (string, bool) {
			if name == "K" {
				return "k", true
			}

			return "", false
		},
		ReadFile: func(name string) ([]byte, error) {
			if body, ok := all[name]; ok {
				return []byte(body), nil
			}

			return nil, io.ErrUnexpectedEOF
		},
		Connect: func(context.Context, *bootstrap.Connection, bootstrap.LoadOptions) (segmentadmin.Store, error) {
			return store, nil
		},
	}

	err := segment.Run(t.Context(), args, &helper.Streams{Stdout: &out, Stderr: &errOut}, &deps)

	return &outcome{stdout: out.String(), stderr: errOut.String(), err: err}
}

func s3Common(config string) []string {
	return []string{flagConfig, config, flagSegments, segmentsFile, flagSegment, segVIP}
}

// TestRun_s3Source covers import and sync from objects, and the resolution of the connection of the source.
func TestRun_s3Source(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr     error
		name        string
		config      string
		extra       []string
		args        []string
		wantOut     []string
		wantMembers []string
		wantCode    int
		wantRecord  bool
	}{
		{
			name:   "import of one object",
			config: fileS3,
			args:   []string{cmdImport, flagFrom, urlIDs},
			wantOut: []string{
				"read: 2",
				wantAdded2,
				wantSwapped,
			},
			wantMembers: []string{"a", "b"},
			wantRecord:  true,
		},
		{
			name: "import of a prefix", config: fileS3, args: []string{cmdImport, flagFrom, urlPrefix},
			wantOut: []string{"read: 4", "added: 4"}, wantMembers: []string{"a", "b", "c", "d"}, wantRecord: true,
		},
		{
			name: "import with a source connection by name", config: "two.yaml",
			args:    []string{cmdImport, flagFrom, urlIDs, flagSrcCon, "src"},
			wantOut: []string{wantAdded2}, wantMembers: []string{"a", "b"}, wantRecord: true,
		},
		{
			name: "import with two s3 connections and no name", config: "two.yaml",
			args: []string{cmdImport, flagFrom, urlIDs}, wantErr: segment.ErrSourceConnection,
			wantCode: helper.ExitInvalid, wantMembers: []string{memberOld},
		},
		{
			name: "import with no s3 connection", config: "none.yaml",
			args: []string{cmdImport, flagFrom, urlIDs}, wantErr: segment.ErrSourceConnection,
			wantCode: helper.ExitInvalid,
		},
		{
			name: "import with a connection that is not s3", config: fileS3,
			args:    []string{cmdImport, flagFrom, urlIDs, flagSrcCon, "fast"},
			wantErr: segment.ErrSourceConnection, wantCode: helper.ExitInvalid,
		},
		{
			name: "import of another bucket", config: fileS3,
			args:    []string{cmdImport, flagFrom, "s3://elsewhere/ids/list.txt"},
			wantErr: segment.ErrSourceConnection, wantCode: helper.ExitInvalid,
		},
		{
			name: "import of an url that is not s3", config: fileS3,
			args:    []string{cmdImport, flagFrom, "s3://"},
			wantErr: segmentadmin.ErrObjectURL, wantCode: helper.ExitInvalid,
		},
		{
			name: "import of a missing object", config: fileS3,
			args:    []string{cmdImport, flagFrom, "s3://bkt/ids/none.txt"},
			wantErr: segmentadmin.ErrObjectMissing, wantCode: helper.ExitFailure, wantMembers: []string{memberOld},
		},
		{
			name: "import of too many objects", config: fileS3,
			args:    []string{cmdImport, flagFrom, urlPrefix, "--max-objects", "1"},
			wantErr: segmentadmin.ErrTooManyObjects, wantCode: helper.ExitInvalid, wantMembers: []string{memberOld},
		},
		{
			name: "sync once imports a changed source", config: fileS3,
			args: []string{cmdSync, flagFrom, urlIDs, flagOnce}, wantMembers: []string{"a", "b"}, wantRecord: true,
		},
		{
			name: "sync once of a source that is not s3", config: fileS3,
			args:    []string{cmdSync, flagFrom, fileIDs, flagOnce},
			wantErr: segmentadmin.ErrObjectURL, wantCode: helper.ExitFailure,
		},
		{
			name: "sync once of a source that is empty", config: fileS3,
			args:    []string{cmdSync, flagFrom, "s3://bkt/nothing/", flagOnce},
			wantErr: segmentadmin.ErrEmptyInput, wantCode: helper.ExitInvalid, wantMembers: []string{memberOld},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, client := admintest.New(), newFakeS3()
			_, err := store.SAdd(t.Context(), liveKey, []string{memberOld}, 0)
			require.NoError(t, err)

			client.put(keyList, "a\nb\n")
			client.put("ids/more.txt", "c\nd\n")

			args := append(slices.Clone(tt.args), s3Common(tt.config)...)
			got := runS3(t, store, client, s3Files(), args...)

			if tt.wantErr != nil {
				require.ErrorIs(t, got.err, tt.wantErr)
				assert.Equal(t, tt.wantCode, helper.ExitCodeOf(got.err))
			} else {
				require.NoError(t, got.err)
			}

			for _, want := range tt.wantOut {
				assert.Contains(t, got.stdout, want)
			}

			if tt.wantMembers != nil {
				assert.Equal(t, tt.wantMembers, store.Members(liveKey))
			}

			_, found, valueErr := store.Value(t.Context(), sourceKey)
			require.NoError(t, valueErr)
			assert.Equal(t, tt.wantRecord, found)
		})
	}
}

// TestRun_syncOnce checks the rounds of sync --once: a second run of an unchanged source writes nothing, a replaced
// object is imported, and a failing source is the exit code of the command.
func TestRun_syncOnce(t *testing.T) {
	t.Parallel()

	store, client := admintest.New(), newFakeS3()
	client.put(keyList, "a\nb\n")

	args := append([]string{cmdSync, flagFrom, urlIDs, flagOnce}, s3Common(fileS3)...)

	first := runS3(t, store, client, s3Files(), args...)
	require.NoError(t, first.err)
	assert.Contains(t, first.stderr, "outcome=changed")
	assert.Equal(t, []string{"a", "b"}, store.Members(liveKey))

	before := len(store.Calls())
	second := runS3(t, store, client, s3Files(), args...)

	require.NoError(t, second.err)
	assert.Contains(t, second.stderr, "outcome=unchanged")
	assert.Equal(t, []string{"PING ", "GET " + sourceKey}, store.Calls()[before:], "the second run writes nothing")

	client.put(keyList, "a\nz\n")

	third := runS3(t, store, client, s3Files(), args...)
	require.NoError(t, third.err)
	assert.Contains(t, third.stderr, "outcome=changed")
	assert.Equal(t, []string{"a", "z"}, store.Members(liveKey))

	client.mu.Lock()
	delete(client.objs, keyList)
	client.mu.Unlock()

	failed := runS3(t, store, client, s3Files(), args...)
	require.ErrorIs(t, failed.err, segmentadmin.ErrObjectMissing)
	assert.Equal(t, helper.ExitFailure, helper.ExitCodeOf(failed.err))
	assert.Contains(t, failed.stderr, "outcome=failed")
	assert.Equal(t, []string{"a", "z"}, store.Members(liveKey), "the previous set is kept")
}

func TestRun_syncLoopStopsWithTheContext(t *testing.T) {
	t.Parallel()

	store, client := admintest.New(), newFakeS3()
	client.put(keyList, "a\n")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var out, errOut bytes.Buffer

	deps := segment.Deps{
		LookupEnv: func(string) (string, bool) { return "", false },
		ReadFile:  func(name string) ([]byte, error) { return []byte(s3Files()[name]), nil },
		Connect: func(context.Context, *bootstrap.Connection, bootstrap.LoadOptions) (segmentadmin.Store, error) {
			return store, nil
		},
		NewS3: func(context.Context, *connection.S3Spec, string) (segment.S3Client, error) { return client, nil },
	}

	args := append([]string{cmdSync, flagFrom, urlIDs, "--interval", "1h"}, s3Common(fileS3)...)

	// a cancelled context ends the loop before its first round; the store is not even reached
	err := segment.Run(ctx, args, &helper.Streams{Stdout: &out, Stderr: &errOut}, &deps)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// writePlane writes a manifest and a segments object to a directory and returns the bootstrap file that reads them.
func writePlane(t *testing.T, objects int, doc, objConn string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	manifest := controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: "Manifest", Channel: "stable", Generation: 1,
	}

	for i := range objects {
		data := []byte(doc)
		name := "seg" + string(rune('a'+i))

		require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), data, 0o600))

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: controlplane.KindSegments, Connection: objConn, Key: name + ".yaml", Format: "yaml",
			Checksum: controlplane.ChecksumOf(data), Size: int64(len(data)),
		})
	}

	raw, err := json.Marshal(&manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o600))

	return dir, `version: 2
connections:
  fast: {type: valkey, addresses: ["127.0.0.1:6380"]}
  plane: {type: file, path: ` + dir + `}
controlPlane:
  connection: plane
  manifest: {key: manifest.json}
`
}

// TestRun_segmentsFromTheControlPlane resolves the segment from the published manifest, without --segments.
func TestRun_segmentsFromTheControlPlane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr   error
		corrupt   func(dir string)
		name      string
		doc       string
		objConn   string
		bootstrap string
		wantKey   string
		objects   int
	}{
		{name: "a segment of the manifest", objects: 1, doc: segmentsDoc, wantKey: liveKey},
		{name: "a name that two objects define", objects: 2, doc: segmentsDoc, wantErr: segment.ErrAmbiguous},
		{
			name:    "an object that is not the listed one",
			objects: 1,
			doc:     segmentsDoc,
			wantErr: controlplane.ErrChecksumMismatch,
			corrupt: func(dir string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "sega.yaml"),
					[]byte(strings.Replace(segmentsDoc, "vip", "vap", 1)), 0o600))
			},
		},
		{name: "a manifest that is gone", objects: 1, doc: segmentsDoc, wantErr: manifestreader.ErrNotFound,
			corrupt: func(dir string) { require.NoError(t, os.Remove(filepath.Join(dir, "manifest.json"))) }},
		{name: "a manifest without a segments object", objects: 0, wantErr: segment.ErrNoSegmentsObject},
		{name: "a bootstrap file without a control plane", bootstrap: bareDoc, wantErr: segment.ErrNoControlPlane},
		{
			name:    "an object on a connection that holds none",
			objects: 1,
			doc:     segmentsDoc,
			objConn: "ghost",
			wantErr: segment.ErrNoStore,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bootstrapDoc := tt.bootstrap

			if bootstrapDoc == "" {
				dir, doc := writePlane(t, tt.objects, tt.doc, cmp.Or(tt.objConn, "plane"))
				bootstrapDoc = doc

				if tt.corrupt != nil {
					tt.corrupt(dir)
				}
			}

			store := admintest.New()
			args := []string{cmdImport, flagFrom, "-", flagConfig, "c.yaml", flagSegment, segVIP}

			var out, errOut bytes.Buffer

			deps := segment.Deps{
				LookupEnv: func(string) (string, bool) { return "", false },
				ReadFile:  func(string) ([]byte, error) { return []byte(bootstrapDoc), nil },
				Stdin:     strings.NewReader("a\nb\n"),
				Connect: func(context.Context, *bootstrap.Connection, bootstrap.LoadOptions) (segmentadmin.Store, error) {
					return store, nil
				},
			}

			err := segment.Run(t.Context(), args, &helper.Streams{Stdout: &out, Stderr: &errOut}, &deps)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr, "%v", err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, []string{"a", "b"}, store.Members(tt.wantKey))
		})
	}
}

// TestRun_noMemberIdInOutputFromS3 is the canary test of the commands that read objects.
func TestRun_noMemberIdInOutputFromS3(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		bad  bool
	}{
		{name: "import of an object", args: []string{cmdImport, flagFrom, urlIDs}},
		{name: "import of a prefix", args: []string{cmdImport, flagFrom, urlPrefix}},
		{name: "sync once", args: []string{cmdSync, flagFrom, urlPrefix, flagOnce}},
		{name: "sync once with a failing store", args: []string{cmdSync, flagFrom, urlPrefix, flagOnce}, bad: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, client := admintest.New(), newFakeS3()
			client.put(keyList, canary+"\n")
			client.put("ids/more.txt", "x,"+canary+"\n")

			if tt.bad {
				store.Fail("SADD", 1)
			}

			args := append(slices.Clone(tt.args), append(s3Common(fileS3), flagDebug)...)
			got := runS3(t, store, client, s3Files(), args...)

			assert.NotContains(t, strings.ToLower(got.all()), strings.ToLower(canary))

			for _, call := range append(store.Calls(), client.calls...) {
				assert.NotContains(t, strings.ToLower(call), strings.ToLower(canary))
			}
		})
	}
}
