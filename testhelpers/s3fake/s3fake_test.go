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

const manifestKey = "m.yaml"

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

	head, err := fake.HeadObject(t.Context(), &s3.HeadObjectInput{Key: aws.String("k")})
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
		Channel: "stable", Generation: 1,
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
