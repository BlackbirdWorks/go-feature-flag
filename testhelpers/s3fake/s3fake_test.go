package s3fake_test

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

const (
	mineBucket    = "mine"
	manifestKey   = "m.yaml"
	stableChannel = "stable"
)

func put(t *testing.T, fake *s3fake.Fake, key string, body []byte, mutate func(*s3.PutObjectInput)) error {
	t.Helper()

	input := &s3.PutObjectInput{Bucket: aws.String("b"), Key: aws.String(key), Body: bytes.NewReader(body)}
	if mutate != nil {
		mutate(input)
	}

	_, err := fake.PutObject(t.Context(), input)

	return err
}

func code(t *testing.T, err error) string {
	t.Helper()

	apiErr, ok := errors.AsType[smithy.APIError](err)
	require.True(t, ok, "%v", err)

	return apiErr.ErrorCode()
}

func TestConditionalWrites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		first    func(*s3.PutObjectInput)
		second   func(etag string) func(*s3.PutObjectInput)
		wantCode string
	}{
		{
			name:  "create-only twice",
			first: func(in *s3.PutObjectInput) { in.IfNoneMatch = aws.String("*") },
			second: func(string) func(*s3.PutObjectInput) {
				return func(in *s3.PutObjectInput) { in.IfNoneMatch = aws.String("*") }
			},
			wantCode: "PreconditionFailed",
		},
		{
			name:  "stale etag",
			first: nil,
			second: func(string) func(*s3.PutObjectInput) {
				return func(in *s3.PutObjectInput) { in.IfMatch = aws.String(`"stale"`) }
			},
			wantCode: "PreconditionFailed",
		},
		{
			name:  "current etag succeeds",
			first: nil,
			second: func(etag string) func(*s3.PutObjectInput) {
				return func(in *s3.PutObjectInput) { in.IfMatch = aws.String(etag) }
			},
		},
		{
			name:  "wrong checksum",
			first: nil,
			second: func(string) func(*s3.PutObjectInput) {
				return func(in *s3.PutObjectInput) { in.ChecksumSHA256 = aws.String("AAAA") }
			},
			wantCode: "BadDigest",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			require.NoError(t, put(t, fake, "k", []byte("one"), tt.first))

			err := put(t, fake, "k", []byte("two"), tt.second(fake.ETag("k")))
			if tt.wantCode == "" {
				require.NoError(t, err)

				body, _ := fake.Body("k")
				assert.Equal(t, []byte("two"), body)

				return
			}

			require.Error(t, err)
			assert.Equal(t, tt.wantCode, code(t, err))

			body, _ := fake.Body("k")
			assert.Equal(t, []byte("one"), body, "a refused write changes nothing")
		})
	}
}

func TestIfMatchOnMissingKey(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)

	err := put(t, fake, "absent", []byte("x"), func(in *s3.PutObjectInput) { in.IfMatch = aws.String(`"e"`) })
	require.ErrorAs(t, err, new(*types.NoSuchKey))
}

func TestGetAndHead(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	etag := fake.Seed("k", []byte("hello"))

	out, err := fake.GetObject(t.Context(), &s3.GetObjectInput{Key: aws.String("k")})
	require.NoError(t, err)

	body, err := io.ReadAll(out.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), body)
	assert.Equal(t, etag, aws.ToString(out.ETag))

	head, err := fake.HeadObject(
		t.Context(),
		&s3.HeadObjectInput{Key: aws.String("k"), ChecksumMode: types.ChecksumModeEnabled},
	)
	require.NoError(t, err)
	assert.Equal(t, int64(5), aws.ToInt64(head.ContentLength))
	assert.NotEmpty(t, aws.ToString(head.ChecksumSHA256))

	_, err = fake.GetObject(t.Context(), &s3.GetObjectInput{Key: aws.String("nope")})
	require.ErrorAs(t, err, new(*types.NoSuchKey))

	_, err = fake.HeadObject(t.Context(), &s3.HeadObjectInput{Key: aws.String("nope")})
	require.ErrorAs(t, err, new(*types.NotFound))
}

func TestCallsAreRecordedInOrderAndHookCanFail(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	fake.Before = func(call string) error {
		if call == "HEAD k" {
			return s3fake.APIError("SlowDown")
		}

		return nil
	}

	require.NoError(t, put(t, fake, "k", []byte("x"), func(in *s3.PutObjectInput) { in.IfNoneMatch = aws.String("*") }))
	require.NoError(
		t,
		put(t, fake, "k", []byte("y"), func(in *s3.PutObjectInput) { in.IfMatch = aws.String(fake.ETag("k")) }),
	)
	require.NoError(t, put(t, fake, "u", []byte("z"), nil))

	_, err := fake.HeadObject(t.Context(), &s3.HeadObjectInput{Key: aws.String("k")})
	require.Error(t, err)
	assert.Equal(t, "SlowDown", code(t, err))

	calls := fake.Calls()
	require.Len(t, calls, 4)
	assert.Equal(t, "PUT k create", calls[0])
	assert.Contains(t, calls[1], "PUT k if-match=")
	assert.Equal(t, "PUT u", calls[2])
	assert.Equal(t, "HEAD k", calls[3])

	fake.ResetCalls()
	assert.Empty(t, fake.Calls())
	assert.Equal(t, []string{"k", "u"}, fake.Keys())

	fake.Remove("u")
	assert.Equal(t, []string{"k"}, fake.Keys())
	assert.Empty(t, fake.ETag("u"))
}

func TestManifestInvariant(t *testing.T) {
	t.Parallel()

	body := []byte("object")
	sum := controlplane.ChecksumOf(body)

	manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
		Channel: stableChannel, Generation: 1,
		Objects: []controlplane.ManifestObject{{
			Name: "a", Kind: controlplane.KindFlags, Connection: "c", Key: "objects/a", Format: controlplane.FormatYAML,
			Checksum: sum, Size: int64(len(body)),
		}},
	})
	require.NoError(t, err)

	t.Run("listed object exists", func(t *testing.T) {
		t.Parallel()

		fake := s3fake.New(manifestKey)
		fake.Seed("objects/a", body)
		require.NoError(t, put(t, fake, manifestKey, manifest, nil))
		fake.AssertConsistent(t)
		assert.Empty(t, fake.Violations())
	})

	t.Run("violations are recorded", func(t *testing.T) {
		t.Parallel()

		fake := s3fake.New(manifestKey)
		require.NoError(t, put(t, fake, manifestKey, manifest, nil))
		require.NoError(t, put(t, fake, manifestKey, []byte("not a manifest"), nil))

		violations := fake.Violations()
		require.Len(t, violations, 2)
		assert.Equal(t, "generation 1 lists missing objects/a", violations[0])
		assert.Contains(t, violations[1], "unreadable manifest")
	})
}

func TestETagIsTheQuotedHexMD5LikeS3(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	etag := fake.Seed("k", []byte("hello"))

	assert.Equal(t, `"5d41402abc4b2a76b9719d911017c592"`, etag, "md5 of hello, no part count")
	assert.Equal(t, etag, fake.Seed("same", []byte("hello")), "the same bytes have the same ETag")
	assert.NotEqual(t, etag, fake.Seed("other", []byte("hellO")))
}

func TestIfNoneMatchOtherThanStarIsNotImplemented(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)

	err := put(t, fake, "k", []byte("x"), func(in *s3.PutObjectInput) { in.IfNoneMatch = aws.String(`"abc"`) })
	require.Error(t, err)
	assert.Equal(t, "NotImplemented", code(t, err))
	assert.Empty(t, fake.Keys(), "nothing is stored")
}

func TestHeadReportsTheChecksumOnlyWhenAskedAndNotOmitted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		mode         types.ChecksumMode
		omit         bool
		wantChecksum bool
	}{
		{name: "enabled", mode: types.ChecksumModeEnabled, wantChecksum: true},
		{name: "unset: S3 needs the mode to report it"},
		{name: "omitted by the store even when enabled", mode: types.ChecksumModeEnabled, omit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			fake.OmitChecksums = tt.omit
			fake.Seed("k", []byte("x"))

			head, err := fake.HeadObject(t.Context(), &s3.HeadObjectInput{Key: aws.String("k"), ChecksumMode: tt.mode})
			require.NoError(t, err)
			assert.Equal(t, tt.wantChecksum, head.ChecksumSHA256 != nil)
			assert.Equal(t, int64(1), aws.ToInt64(head.ContentLength))
		})
	}
}

func TestVersionedGet(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	fake.Seed("k", []byte("latest"))
	fake.SeedVersion("k", "v1", []byte("old"))

	out, err := fake.GetObject(t.Context(), &s3.GetObjectInput{Key: aws.String("k"), VersionId: aws.String("v1")})
	require.NoError(t, err)

	body, err := io.ReadAll(out.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("old"), body)

	_, err = fake.GetObject(t.Context(), &s3.GetObjectInput{Key: aws.String("k"), VersionId: aws.String("v9")})
	require.Error(t, err)
	assert.Equal(t, "NoSuchVersion", code(t, err))
	assert.Contains(t, fake.Calls(), "GET k version=v1")
}

func TestStatusErrorIsAnAPIErrorWithAnHTTPStatus(t *testing.T) {
	t.Parallel()

	err := error(&s3fake.StatusError{Code: "NoSuchBucket", Status: 404})

	assert.Equal(t, "NoSuchBucket", code(t, err))
	assert.Equal(t, "404 NoSuchBucket", err.Error())

	statusErr, ok := errors.AsType[*s3fake.StatusError](err)
	require.True(t, ok)
	assert.Equal(t, 404, statusErr.HTTPStatusCode())
	assert.Equal(t, "NoSuchBucket", statusErr.ErrorMessage())
	assert.Equal(t, smithy.FaultClient, statusErr.ErrorFault())
}

func TestInvariantChecksSizeAndChecksumOfListedObjects(t *testing.T) {
	t.Parallel()

	body := []byte("object")
	listed := func(size int64, sum controlplane.Checksum) []byte {
		manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
			Channel: stableChannel, Generation: 1,
			Objects: []controlplane.ManifestObject{
				{
					Name:       "a",
					Kind:       controlplane.KindFlags,
					Connection: "c",
					Key:        "objects/a",
					Format:     controlplane.FormatYAML,
					Checksum:   sum,
					Size:       size,
				},
			},
		})
		require.NoError(t, err)

		return manifest
	}

	tests := []struct {
		name string
		want string
		body []byte
	}{
		{
			name: "wrong size",
			body: listed(int64(len(body))+1, controlplane.ChecksumOf(body)),
			want: "with size 7, stored 6",
		},
		{
			name: "wrong checksum",
			body: listed(int64(len(body)), controlplane.ChecksumOf([]byte("other!"))),
			want: "another checksum",
		},
		{name: "consistent", body: listed(int64(len(body)), controlplane.ChecksumOf(body))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			fake.Seed("objects/a", body)
			require.NoError(t, put(t, fake, manifestKey, tt.body, nil))

			violations := fake.Violations()
			if tt.want == "" {
				assert.Empty(t, violations)

				return
			}

			require.Len(t, violations, 1)
			assert.Contains(t, violations[0], tt.want)
		})
	}
}

func TestInvariantChecksThePinnedVersion(t *testing.T) {
	t.Parallel()

	const object = "objects/a"

	pinned := []byte("pinned version")
	latest := []byte("a different latest")

	manifestPinning := func(t *testing.T, versionID string) []byte {
		t.Helper()

		out, err := controlplane.MarshalManifest(&controlplane.Manifest{
			Channel: stableChannel, Generation: 1,
			Objects: []controlplane.ManifestObject{
				{
					Name:       "a",
					Kind:       controlplane.KindFlags,
					Connection: "c",
					Key:        object,
					Format:     controlplane.FormatYAML,
					Checksum:   controlplane.ChecksumOf(pinned),
					Size:       int64(len(pinned)),
					VersionID:  versionID,
				},
			},
		})
		require.NoError(t, err)

		return out
	}

	tests := []struct {
		name          string
		seed          func(f *s3fake.Fake)
		versionID     string
		wantViolation string
	}{
		{
			name: "the pinned version is intact while the latest differs", versionID: "v1",
			seed: func(f *s3fake.Fake) {
				f.Seed(object, latest)
				f.SeedVersion(object, "v1", pinned)
			},
		},
		{
			name: "the pinned version is missing while the latest matches", versionID: "v9",
			seed: func(f *s3fake.Fake) {
				f.Seed(object, pinned)
				f.SeedVersion(object, "v1", pinned)
			},
			wantViolation: "lists missing objects/a",
		},
		{
			name: "the pinned version has other bytes", versionID: "v1",
			seed: func(f *s3fake.Fake) {
				f.Seed(object, pinned)
				f.SeedVersion(object, "v1", []byte("pinned versiOn"))
			},
			wantViolation: "another checksum",
		},
		{
			name: "an unpinned entry still reads the latest object", versionID: "",
			seed: func(f *s3fake.Fake) { f.Seed(object, pinned) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			tt.seed(fake)
			require.NoError(t, put(t, fake, manifestKey, manifestPinning(t, tt.versionID), nil))

			violations := fake.Violations()
			if tt.wantViolation == "" {
				assert.Empty(t, violations)

				return
			}

			require.Len(t, violations, 1)
			assert.Contains(t, violations[0], tt.wantViolation)
		})
	}
}

func TestOnlyTheConfiguredBucketExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fakeBucket string
		bucket     string
		wantCode   string
	}{
		{name: "the configured bucket", fakeBucket: mineBucket, bucket: mineBucket},
		{name: "another bucket is a missing bucket", fakeBucket: mineBucket, bucket: "yours", wantCode: "NoSuchBucket"},
		{name: "no configured bucket accepts any", fakeBucket: "", bucket: "anything"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			fake.Bucket = tt.fakeBucket
			fake.Seed("k", []byte("x"))

			calls := map[string]func() error{
				"get": func() error {
					_, err := fake.GetObject(
						t.Context(),
						&s3.GetObjectInput{Bucket: aws.String(tt.bucket), Key: aws.String("k")},
					)

					return err
				},
				"head": func() error {
					_, err := fake.HeadObject(
						t.Context(),
						&s3.HeadObjectInput{Bucket: aws.String(tt.bucket), Key: aws.String("k")},
					)

					return err
				},
				"put": func() error {
					return put(
						t,
						fake,
						"k2",
						[]byte("y"),
						func(in *s3.PutObjectInput) { in.Bucket = aws.String(tt.bucket) },
					)
				},
			}

			for name, call := range calls {
				err := call()

				if tt.wantCode == "" {
					require.NoError(t, err, name)

					continue
				}

				require.Error(t, err, name)
				assert.Equal(t, tt.wantCode, code(t, err), name)
			}

			if tt.wantCode != "" {
				assert.NotContains(t, fake.Keys(), "k2", "a write to the wrong bucket stores nothing")
			}
		})
	}
}
