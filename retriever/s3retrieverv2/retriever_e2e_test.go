//go:build docker

package s3retrieverv2_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

const (
	fileAJSON = "f/a.json"
	flagA     = "flag-a"
	flagB     = "flag-b"
	flagC     = "flag-c"
	flagD     = "flag-d"
	flagRoot  = "flag-root"
	flagsJSON = "flags.json"
)

func flagJSON(key, value string) []byte {
	return []byte(`{"` + key + `":{"variations":{"a":"` + value + `","b":"other"},"defaultRule":{"variation":"a"}}}`)
}

func flagYAML(key, value string) []byte {
	return []byte(key + ":\n  variations:\n    a: " + value + "\n    b: other\n  defaultRule:\n    variation: a\n")
}

func flagKeys(t *testing.T, content []byte) []string {
	t.Helper()

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(content, &m))

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

func TestE2E_SingleItem(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)

	tests := []struct {
		name        string
		key         string
		itemToRead  string // defaults to key
		wantErr     string
		content     []byte
		wantContent []byte
		useMissing  bool // read from a bucket that does not exist
	}{
		{
			name: "small json item", key: flagsJSON,
			content: flagJSON("f1", "v1"), wantContent: flagJSON("f1", "v1"),
		},
		{
			name: "nested key", key: "a/b/c/flags.yaml",
			content: flagYAML("f1", "v1"), wantContent: flagYAML("f1", "v1"),
		},
		{name: "empty object", key: "empty.json", content: []byte{}, wantContent: []byte{}},
		{
			name: "larger object over several MB (multipart ranges)", key: "big.json",
			content:     bytes.Repeat([]byte("0123456789abcdef"), 6*1024*1024/16),
			wantContent: bytes.Repeat([]byte("0123456789abcdef"), 6*1024*1024/16),
		},
		{
			name:       "missing object",
			key:        "present.json",
			content:    flagJSON("f1", "v1"),
			itemToRead: "absent.json",
			wantErr:    "unable to download item from S3",
		},
		{
			name:       "missing bucket",
			key:        "present.json",
			content:    flagJSON("f1", "v1"),
			useMissing: true,
			wantErr:    "unable to download item from S3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bucket := srv.CreateBucket(t, "ret")
			srv.PutObject(t, bucket, tt.key, tt.content)
			cfg := srv.MustAwsConfig(t)

			item := tt.itemToRead
			if item == "" {
				item = tt.key
			}

			if tt.useMissing {
				bucket = gopherstack.UniqueName(t, "nobucket")
			}

			r := &s3retrieverv2.Retriever{Bucket: bucket, Item: item, AwsConfig: &cfg}
			require.NoError(t, r.Init(context.Background(), slog.New(slog.DiscardHandler)))
			t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
			assert.Equal(t, retriever.RetrieverReady, r.Status())

			got, err := r.Retrieve(context.Background())
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.True(
				t,
				bytes.Equal(tt.wantContent, got),
				"content mismatch: want %d bytes, got %d",
				len(tt.wantContent),
				len(got),
			)
		})
	}
}

func TestE2E_SingleItem_UpdateVisibleOnNextRetrieve(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "upd")
	cfg := srv.MustAwsConfig(t)

	steps := []struct {
		name string
		put  []byte
		want []byte
	}{
		{name: "first version", put: flagJSON("f1", "v1"), want: flagJSON("f1", "v1")},
		{
			name: "second version, other size",
			put:  flagJSON("f1", "a-much-longer-value-v2"), want: flagJSON("f1", "a-much-longer-value-v2"),
		},
		{
			name: "third version, replaced by other flag",
			put:  flagJSON("f2", "v3"), want: flagJSON("f2", "v3"),
		},
	}
	r := &s3retrieverv2.Retriever{Bucket: bucket, Item: flagsJSON, AwsConfig: &cfg}
	require.NoError(t, r.Init(context.Background(), slog.New(slog.DiscardHandler)))
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	// steps are sequential by nature: each one depends on the previous object version.
	for _, s := range steps {
		srv.PutObject(t, bucket, flagsJSON, s.put)

		got, err := r.Retrieve(context.Background())
		require.NoError(t, err, s.name)
		assert.Equal(t, string(s.want), string(got), s.name)
	}
}

func TestE2E_Retriever_NotFoundBeforeCreation(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "late")
	cfg := srv.MustAwsConfig(t)
	r := &s3retrieverv2.Retriever{Bucket: bucket, Item: "late.json", AwsConfig: &cfg}
	require.NoError(t, r.Init(context.Background(), slog.New(slog.DiscardHandler)))
	_, err := r.Retrieve(context.Background())
	require.Error(t, err)

	srv.PutObject(t, bucket, "late.json", flagJSON("f1", "v1"))

	got, err := r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, string(flagJSON("f1", "v1")), string(got))
}

func TestE2E_FolderMode(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)

	objects := map[string][]byte{
		fixtures.FlagsAYAML:     flagYAML(flagA, "1"),
		fixtures.FlagsBYAML:     flagYAML(flagB, "1"),
		"flags/notes.txt":       []byte("not a flag file"),
		fixtures.FlagsSubCYAML:  flagYAML(flagC, "1"),
		"flags/sub/deep/d.yaml": flagYAML(flagD, "1"),
		"other/e.yaml":          flagYAML("flag-e", "1"),
		"root.yaml":             flagYAML(flagRoot, "1"),
	}

	tests := []struct {
		name      string
		prefix    string
		pattern   string
		wantErr   string
		wantKeys  []string
		recursive bool
	}{
		{name: "prefix direct children only", prefix: fixtures.FlagsDirSlash, wantKeys: []string{flagA, flagB}},
		{name: "prefix without trailing slash", prefix: fixtures.FlagsDir, wantKeys: []string{flagA, flagB}},
		{
			name:      "prefix recursive",
			prefix:    fixtures.FlagsDirSlash,
			recursive: true,
			wantKeys:  []string{flagA, flagB, flagC, flagD},
		},
		{
			name:      "pattern restricts file names",
			prefix:    fixtures.FlagsDirSlash,
			pattern:   "a.*",
			recursive: true,
			wantKeys:  []string{flagA},
		},
		{
			name:      "pattern matches basename in sub folders",
			prefix:    fixtures.FlagsDirSlash,
			pattern:   "[cd].yaml",
			recursive: true,
			wantKeys:  []string{flagC, flagD},
		},
		{
			name:     "pattern without match gives empty set",
			prefix:   fixtures.FlagsDirSlash,
			pattern:  "zzz*.yaml",
			wantKeys: []string{},
		},
		{name: "root prefix not recursive", prefix: "", pattern: fixtures.YAMLGlob, wantKeys: []string{flagRoot}},
		{
			name:      "root prefix recursive",
			prefix:    "/",
			pattern:   fixtures.YAMLGlob,
			recursive: true,
			wantKeys:  []string{flagA, flagB, flagC, flagD, "flag-e", flagRoot},
		},
		{name: "unknown prefix gives empty set", prefix: "nothing/", wantKeys: []string{}},
		{
			name:    "pattern with slash is rejected",
			prefix:  fixtures.FlagsDirSlash,
			pattern: "sub/*.yaml",
			wantErr: "impossible to init S3 retriever v2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bucket := srv.CreateBucket(t, "dir")
			for k, v := range objects {
				srv.PutObject(t, bucket, k, v)
			}

			cfg := srv.MustAwsConfig(t)
			r := &s3retrieverv2.Retriever{
				Bucket: bucket, Prefix: tt.prefix, Pattern: tt.pattern, Recursive: tt.recursive, AwsConfig: &cfg,
			}

			err := r.Init(context.Background(), slog.New(slog.DiscardHandler))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}

			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Shutdown(context.Background()) })

			got, err := r.Retrieve(context.Background())
			require.NoError(t, err)
			// the merged set is re-encoded in the format of the files, "yaml" here (json when empty)
			if len(tt.wantKeys) == 0 {
				assert.Equal(t, "json", r.OutputFormat())
				assert.Equal(t, []string{}, flagKeys(t, got))

				return
			}

			assert.Equal(t, "yaml", r.OutputFormat())

			for _, k := range tt.wantKeys {
				assert.Contains(t, string(got), k+":")
			}

			assert.Equal(t, len(tt.wantKeys), strings.Count(string(got), "variations:"), "unexpected flags in %s", got)
		})
	}
}

func TestE2E_FolderMode_ChangesAreSeen(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "dirchg")
	cfg := srv.MustAwsConfig(t)
	srv.PutObject(t, bucket, fileAJSON, flagJSON(flagA, "v1"))

	r := &s3retrieverv2.Retriever{Bucket: bucket, Prefix: "f/", Pattern: "*.json", AwsConfig: &cfg}
	require.NoError(t, r.Init(context.Background(), slog.New(slog.DiscardHandler)))
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })

	got, err := r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{flagA}, flagKeys(t, got))

	// a new file appears and the first one is modified
	srv.PutObject(t, bucket, "f/b.json", flagJSON(flagB, "v1"))
	srv.PutObject(t, bucket, fileAJSON, flagJSON(flagA, "v2"))

	got, err = r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{flagA, flagB}, flagKeys(t, got))
	assert.Contains(t, string(got), `"a":"v2"`)

	// a flag defined in two files is an error
	srv.PutObject(t, bucket, "f/c.json", flagJSON(flagA, "dup"))

	_, err = r.Retrieve(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate flag")

	// a deleted file disappears from the merged set
	_, err = srv.S3Client(t).DeleteObject(context.Background(), deleteInput(bucket, "f/c.json"))
	require.NoError(t, err)
	_, err = srv.S3Client(t).DeleteObject(context.Background(), deleteInput(bucket, fileAJSON))
	require.NoError(t, err)
	got, err = r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{flagB}, flagKeys(t, got))
}

func TestE2E_SharedItems_TwoItemsInOneBucket(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)

	tests := []struct {
		name           string
		prefixA        string
		prefixB        string
		wantA          string
		wantB          string
		wantBUpdated   string
		wantRefreshedA int32
	}{
		{
			name:           "same folder",
			prefixA:        fixtures.FlagsDirSlash,
			prefixB:        fixtures.FlagsDirSlash,
			wantA:          string(flagJSON(flagA, "a1")),
			wantB:          string(flagJSON(flagB, "b1")),
			wantBUpdated:   string(flagJSON(flagB, "b2")),
			wantRefreshedA: 0,
		},
		{
			name:           "different folders",
			prefixA:        "team-a/",
			prefixB:        "team-b/",
			wantA:          string(flagJSON(flagA, "a1")),
			wantB:          string(flagJSON(flagB, "b1")),
			wantBUpdated:   string(flagJSON(flagB, "b2")),
			wantRefreshedA: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bucket := srv.CreateBucket(t, "shared")
			cfg := srv.MustAwsConfig(t)
			keyA, keyB := tt.prefixA+"a.json", tt.prefixB+"b.json"
			srv.PutObject(t, bucket, keyA, flagJSON(flagA, "a1"))
			srv.PutObject(t, bucket, keyB, flagJSON(flagB, "b1"))
			// an unrelated object under the listing prefix must not be downloaded or break anything
			srv.PutObject(t, bucket, tt.prefixA+"unrelated.txt", []byte("hello"))

			group, err := s3retrieverv2.NewSharedItems(context.Background(), &s3retrieverv2.SharedItemsOptions{
				Bucket: bucket, AwsConfig: &cfg,
			})
			require.NoError(t, err)

			retA, retB := group.Retriever(keyA), group.Retriever(keyB)
			require.NoError(t, retA.Init(context.Background(), slog.New(slog.DiscardHandler)))
			require.NoError(t, retB.Init(context.Background(), slog.New(slog.DiscardHandler)))
			t.Cleanup(func() {
				_ = retA.Shutdown(context.Background())
				_ = retB.Shutdown(context.Background())
			})

			gotA, err := retA.Retrieve(context.Background())
			require.NoError(t, err)
			gotB, err := retB.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.wantA, string(gotA))
			assert.Equal(t, tt.wantB, string(gotB))

			// the poller refreshes only the item whose object changed
			var refreshedA, refreshedB atomic.Int32

			refresh := func(r interface {
				Retrieve(context.Context) ([]byte, error)
			}, refreshes *atomic.Int32) func() bool {
				return func() bool {
					_, retrieveErr := r.Retrieve(context.Background())
					if retrieveErr != nil {
						return false
					}

					refreshes.Add(1)

					return true
				}
			}

			group.SetPoller(s3retrieverv2.PollerConfig{Interval: time.Second})
			group.SetRefresh(retA, refresh(retA, &refreshedA))
			group.SetRefresh(retB, refresh(retB, &refreshedB))

			srv.PutObject(t, bucket, keyB, flagJSON(flagB, "b2"))
			require.Eventually(t, func() bool { return refreshedB.Load() >= 1 }, 15*time.Second, 100*time.Millisecond)
			assert.Equal(t, tt.wantRefreshedA, refreshedA.Load(), "unchanged item must not be refreshed")

			gotB, err = retB.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.wantBUpdated, string(gotB))

			gotA, err = retA.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.wantA, string(gotA))
		})
	}
}

func TestE2E_SharedItems_Errors(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Connect(t)

	tests := []struct {
		name    string
		bucket  func(t *testing.T) string
		item    string
		wantErr string
	}{
		{
			name: "missing object",
			bucket: func(t *testing.T) string {
				t.Helper()

				return srv.CreateBucket(t, "sherr")
			},
			item:    "nope/flags.json",
			wantErr: `item "nope/flags.json" not found in S3 bucket "sherr-`,
		},
		{
			name: "missing bucket",
			bucket: func(t *testing.T) string {
				t.Helper()

				return gopherstack.UniqueName(t, "nobucket")
			},
			item:    "nope/flags.json",
			wantErr: `NoSuchBucket: The specified bucket does not exist.`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			group, err := s3retrieverv2.NewSharedItems(context.Background(), &s3retrieverv2.SharedItemsOptions{
				Bucket: tt.bucket(t), AwsConfig: &cfg,
			})
			require.NoError(t, err)

			r := group.Retriever(tt.item)
			require.NoError(t, r.Init(context.Background(), slog.New(slog.DiscardHandler)))
			t.Cleanup(func() { _ = r.Shutdown(context.Background()) })

			got, err := r.Retrieve(context.Background())
			assert.Nil(t, got)
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func deleteInput(bucket, key string) *s3.DeleteObjectInput {
	return &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
}
