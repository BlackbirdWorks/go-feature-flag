//go:build docker

package s3retrieverv2_test

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
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
	"github.com/blackbirdworks/semaphore/testutils/gopherstack"
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
	sort.Strings(keys)

	return keys
}

func TestE2E_SingleItem(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)

	tests := []struct {
		name        string
		key         string
		content     []byte
		itemToRead  string // defaults to key
		useMissing  bool   // read from a bucket that does not exist
		wantContent bool
		wantErr     string
	}{
		{name: "small json item", key: "flags.json", content: flagJSON("f1", "v1"), wantContent: true},
		{name: "nested key", key: "a/b/c/flags.yaml", content: flagYAML("f1", "v1"), wantContent: true},
		{name: "empty object", key: "empty.json", content: []byte{}, wantContent: true},
		{
			name: "larger object over several MB (multipart ranges)", key: "big.json",
			content: bytes.Repeat([]byte("0123456789abcdef"), 6*1024*1024/16), wantContent: true,
		},
		{name: "missing object", key: "present.json", content: flagJSON("f1", "v1"), itemToRead: "absent.json", wantErr: "unable to download item from S3"},
		{name: "missing bucket", key: "present.json", content: flagJSON("f1", "v1"), useMissing: true, wantErr: "unable to download item from S3"},
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
			require.NoError(t, r.Init(context.Background(), nil))
			t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
			assert.Equal(t, retriever.RetrieverReady, r.Status())

			got, err := r.Retrieve(context.Background())
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)

				return
			}
			require.NoError(t, err)
			assert.True(t, bytes.Equal(tt.content, got), "content mismatch: want %d bytes, got %d", len(tt.content), len(got))
		})
	}
}

func TestE2E_SingleItem_UpdateVisibleOnNextRetrieve(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)
	bucket := srv.CreateBucket(t, "upd")
	cfg := srv.MustAwsConfig(t)

	steps := []struct {
		name    string
		content []byte
	}{
		{name: "first version", content: flagJSON("f1", "v1")},
		{name: "second version, other size", content: flagJSON("f1", "a-much-longer-value-v2")},
		{name: "third version, replaced by other flag", content: flagJSON("f2", "v3")},
	}
	r := &s3retrieverv2.Retriever{Bucket: bucket, Item: "flags.json", AwsConfig: &cfg}
	require.NoError(t, r.Init(context.Background(), nil))
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	// steps are sequential by nature: each one depends on the previous object version.
	for _, s := range steps {
		srv.PutObject(t, bucket, "flags.json", s.content)
		got, err := r.Retrieve(context.Background())
		require.NoError(t, err, s.name)
		assert.Equal(t, string(s.content), string(got), s.name)
	}
}

func TestE2E_Retriever_NotFoundBeforeCreation(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)
	bucket := srv.CreateBucket(t, "late")
	cfg := srv.MustAwsConfig(t)
	r := &s3retrieverv2.Retriever{Bucket: bucket, Item: "late.json", AwsConfig: &cfg}
	require.NoError(t, r.Init(context.Background(), nil))
	_, err := r.Retrieve(context.Background())
	require.Error(t, err)

	srv.PutObject(t, bucket, "late.json", flagJSON("f1", "v1"))
	got, err := r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, string(flagJSON("f1", "v1")), string(got))
}

func TestE2E_FolderMode(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)

	objects := map[string][]byte{
		"flags/a.yaml":          flagYAML("flag-a", "1"),
		"flags/b.yaml":          flagYAML("flag-b", "1"),
		"flags/notes.txt":       []byte("not a flag file"),
		"flags/sub/c.yaml":      flagYAML("flag-c", "1"),
		"flags/sub/deep/d.yaml": flagYAML("flag-d", "1"),
		"other/e.yaml":          flagYAML("flag-e", "1"),
		"root.yaml":             flagYAML("flag-root", "1"),
	}

	tests := []struct {
		name      string
		prefix    string
		pattern   string
		recursive bool
		wantKeys  []string
		wantErr   string
	}{
		{name: "prefix direct children only", prefix: "flags/", wantKeys: []string{"flag-a", "flag-b"}},
		{name: "prefix without trailing slash", prefix: "flags", wantKeys: []string{"flag-a", "flag-b"}},
		{name: "prefix recursive", prefix: "flags/", recursive: true, wantKeys: []string{"flag-a", "flag-b", "flag-c", "flag-d"}},
		{name: "pattern restricts file names", prefix: "flags/", pattern: "a.*", recursive: true, wantKeys: []string{"flag-a"}},
		{name: "pattern matches basename in sub folders", prefix: "flags/", pattern: "[cd].yaml", recursive: true, wantKeys: []string{"flag-c", "flag-d"}},
		{name: "pattern without match gives empty set", prefix: "flags/", pattern: "zzz*.yaml", wantKeys: []string{}},
		{name: "root prefix not recursive", prefix: "", pattern: "*.yaml", wantKeys: []string{"flag-root"}},
		{name: "root prefix recursive", prefix: "/", pattern: "*.yaml", recursive: true, wantKeys: []string{"flag-a", "flag-b", "flag-c", "flag-d", "flag-e", "flag-root"}},
		{name: "unknown prefix gives empty set", prefix: "nothing/", wantKeys: []string{}},
		{name: "pattern with slash is rejected", prefix: "flags/", pattern: "sub/*.yaml", wantErr: "impossible to init S3 retriever v2"},
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
			err := r.Init(context.Background(), nil)
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
	srv := gopherstack.Shared(t)
	bucket := srv.CreateBucket(t, "dirchg")
	cfg := srv.MustAwsConfig(t)
	srv.PutObject(t, bucket, "f/a.json", flagJSON("flag-a", "v1"))

	r := &s3retrieverv2.Retriever{Bucket: bucket, Prefix: "f/", Pattern: "*.json", AwsConfig: &cfg}
	require.NoError(t, r.Init(context.Background(), nil))
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })

	got, err := r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"flag-a"}, flagKeys(t, got))

	// a new file appears and the first one is modified
	srv.PutObject(t, bucket, "f/b.json", flagJSON("flag-b", "v1"))
	srv.PutObject(t, bucket, "f/a.json", flagJSON("flag-a", "v2"))
	got, err = r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"flag-a", "flag-b"}, flagKeys(t, got))
	assert.Contains(t, string(got), `"a":"v2"`)

	// a flag defined in two files is an error
	srv.PutObject(t, bucket, "f/c.json", flagJSON("flag-a", "dup"))
	_, err = r.Retrieve(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate flag")

	// a deleted file disappears from the merged set
	_, err = srv.S3Client(t).DeleteObject(context.Background(), deleteInput(bucket, "f/c.json"))
	require.NoError(t, err)
	_, err = srv.S3Client(t).DeleteObject(context.Background(), deleteInput(bucket, "f/a.json"))
	require.NoError(t, err)
	got, err = r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"flag-b"}, flagKeys(t, got))
}

func TestE2E_SharedItems_TwoItemsInOneBucket(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)

	tests := []struct {
		name      string
		prefixA   string
		prefixB   string
		wantEmpty bool
	}{
		{name: "same folder", prefixA: "flags/", prefixB: "flags/"},
		{name: "different folders", prefixA: "team-a/", prefixB: "team-b/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bucket := srv.CreateBucket(t, "shared")
			cfg := srv.MustAwsConfig(t)
			keyA, keyB := tt.prefixA+"a.json", tt.prefixB+"b.json"
			srv.PutObject(t, bucket, keyA, flagJSON("flag-a", "a1"))
			srv.PutObject(t, bucket, keyB, flagJSON("flag-b", "b1"))
			// an unrelated object under the listing prefix must not be downloaded or break anything
			srv.PutObject(t, bucket, tt.prefixA+"unrelated.txt", []byte("hello"))

			group, err := s3retrieverv2.NewSharedItems(context.Background(), s3retrieverv2.SharedItemsOptions{
				Bucket: bucket, AwsConfig: &cfg,
			})
			require.NoError(t, err)
			ra, rb := group.Retriever(keyA), group.Retriever(keyB)
			require.NoError(t, ra.Init(context.Background(), nil))
			require.NoError(t, rb.Init(context.Background(), nil))
			t.Cleanup(func() {
				_ = ra.Shutdown(context.Background())
				_ = rb.Shutdown(context.Background())
			})

			gotA, err := ra.Retrieve(context.Background())
			require.NoError(t, err)
			gotB, err := rb.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, string(flagJSON("flag-a", "a1")), string(gotA))
			assert.Equal(t, string(flagJSON("flag-b", "b1")), string(gotB))

			// the poller refreshes only the item whose object changed
			var refreshedA, refreshedB atomic.Int32
			refresh := func(r interface {
				Retrieve(context.Context) ([]byte, error)
			}, n *atomic.Int32) func() bool {
				return func() bool {
					if _, err := r.Retrieve(context.Background()); err != nil {
						return false
					}
					n.Add(1)

					return true
				}
			}
			group.SetPoller(s3retrieverv2.PollerConfig{Interval: time.Second})
			group.SetRefresh(ra, refresh(ra, &refreshedA))
			group.SetRefresh(rb, refresh(rb, &refreshedB))

			srv.PutObject(t, bucket, keyB, flagJSON("flag-b", "b2"))
			require.Eventually(t, func() bool { return refreshedB.Load() >= 1 }, 15*time.Second, 100*time.Millisecond)
			assert.Zero(t, refreshedA.Load(), "unchanged item must not be refreshed")

			gotB, err = rb.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, string(flagJSON("flag-b", "b2")), string(gotB))
			gotA, err = ra.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, string(flagJSON("flag-a", "a1")), string(gotA))
		})
	}
}

func TestE2E_SharedItems_Errors(t *testing.T) {
	t.Parallel()
	srv := gopherstack.Shared(t)

	tests := []struct {
		name   string
		bucket func(t *testing.T) string
		item   string
	}{
		{name: "missing object", bucket: func(t *testing.T) string { return srv.CreateBucket(t, "sherr") }, item: "nope/flags.json"},
		{name: "missing bucket", bucket: func(t *testing.T) string { return gopherstack.UniqueName(t, "nobucket") }, item: "nope/flags.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := srv.MustAwsConfig(t)
			group, err := s3retrieverv2.NewSharedItems(context.Background(), s3retrieverv2.SharedItemsOptions{
				Bucket: tt.bucket(t), AwsConfig: &cfg,
			})
			require.NoError(t, err)
			r := group.Retriever(tt.item)
			require.NoError(t, r.Init(context.Background(), nil))
			t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
			_, err = r.Retrieve(context.Background())
			require.Error(t, err)
		})
	}
}

func deleteInput(bucket, key string) *s3.DeleteObjectInput {
	return &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
}
