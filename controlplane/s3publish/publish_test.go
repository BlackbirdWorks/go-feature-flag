package s3publish_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(*s3publish.Config)
		name   string
		nilAPI bool
	}{
		{name: "nil client", nilAPI: true},
		{name: "no bucket", mutate: func(c *s3publish.Config) { c.Bucket = "" }},
		{name: "no channel", mutate: func(c *s3publish.Config) { c.Channel = "" }},
		{name: "no connection", mutate: func(c *s3publish.Config) { c.Connection = "" }},
		{name: "leading slash prefix", mutate: func(c *s3publish.Config) { c.Prefix = "/x" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testConfig()
			if tt.mutate != nil {
				tt.mutate(&cfg)
			}

			var api s3publish.API
			if !tt.nilAPI {
				api = s3fake.New(manifestKey)
			}

			pub, err := s3publish.New(api, &cfg)
			require.ErrorIs(t, err, s3publish.ErrConfig)
			assert.Nil(t, pub)
		})
	}
}

func TestPublishFirst(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	objects := standardObjects(t, "a")
	flagsKey := objectKey(t, objects[0].Body)
	setsKey := objectKey(t, objects[1].Body)

	res, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET " + manifestKey,
		"HEAD " + flagsKey, "PUT " + flagsKey + " create",
		"HEAD " + setsKey, "PUT " + setsKey + " create",
		"PUT " + manifestKey + " create",
	}, fake.Calls())

	assert.Equal(t, s3publish.Requests{Get: 1, Head: 2, Put: 3}, res.Requests)
	assert.Equal(t, 6, res.Requests.Total())
	assert.Equal(t, uint64(1), res.Generation)
	assert.Equal(t, uint64(0), res.Previous)
	assert.True(t, res.Changed)
	assert.True(t, res.Written)
	assert.Equal(t, 2, res.Uploaded)
	assert.Equal(t, 0, res.Present)
	assert.Equal(t, int64(len(objects[0].Body)+len(objects[1].Body)), res.UploadedBytes)
	assert.Equal(t, fake.ETag(manifestKey), res.ManifestETag)
	assert.Equal(t, []s3publish.ObjectResult{
		{Name: nameFlags, Key: flagsKey, Status: s3publish.StatusUploaded, Size: int64(len(objects[0].Body))},
		{Name: nameSets, Key: setsKey, Status: s3publish.StatusUploaded, Size: int64(len(objects[1].Body))},
	}, res.Objects)

	stored, ok := fake.Body(manifestKey)
	require.True(t, ok)

	manifest, _, err := controlplane.UnmarshalManifest(stored)
	require.NoError(t, err)
	require.NoError(t, controlplane.ValidateManifest(manifest))
	assert.Equal(t, uint64(1), manifest.Generation)
	assert.Equal(t, testChannel, manifest.Channel)
	require.Len(t, manifest.Objects, 2)
	assert.Equal(t, controlplane.ManifestObject{
		Name:       nameFlags,
		Kind:       controlplane.KindFlags,
		Connection: testConnection,
		Key:        flagsKey,
		Format:     controlplane.FormatYAML,
		Checksum:   controlplane.ChecksumOf(objects[0].Body),
		Size:       int64(len(objects[0].Body)),
	}, manifest.Objects[0])

	for _, o := range objects {
		body, found := fake.Body(objectKey(t, o.Body))
		require.True(t, found)
		assert.Equal(t, o.Body, body)
	}

	fake.AssertConsistent(t)
}

func TestPublishPrefixAndManifestKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		prefix       string
		manifestKey  string
		wantManifest string
		wantPrefix   string
	}{
		{name: "prefix gets a slash", prefix: "acme", wantManifest: "acme/manifests/stable.yaml", wantPrefix: "acme/"},
		{
			name:         "prefix with slash",
			prefix:       "acme/x/",
			wantManifest: "acme/x/manifests/stable.yaml",
			wantPrefix:   "acme/x/",
		},
		{
			name:         "explicit manifest key",
			prefix:       "acme",
			manifestKey:  "m/prod.json",
			wantManifest: "m/prod.json",
			wantPrefix:   "acme/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			objects := standardObjects(t, "a")

			pub := newPublisher(
				t,
				fake,
				func(c *s3publish.Config) { c.Prefix = tt.prefix; c.ManifestKey = tt.manifestKey },
			)
			assert.Equal(t, tt.wantManifest, pub.ManifestKey())

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
			require.NoError(t, err)

			body, ok := fake.Body(tt.wantManifest)
			require.True(t, ok)

			manifest, _, err := controlplane.UnmarshalManifest(body)
			require.NoError(t, err)

			for _, o := range manifest.Objects {
				assert.Contains(t, o.Key, tt.wantPrefix+"objects/sha256/")
				_, found := fake.Body(o.Key)
				assert.True(t, found, o.Key)
			}

			if tt.manifestKey != "" {
				assert.Equal(t, byte('{'), body[0])
			}
		})
	}
}

func TestPublishRepublish(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	pub := newPublisher(t, fake, nil)
	first := standardObjects(t, "a")

	res1, err := pub.Publish(t.Context(), s3publish.Request{Objects: first})
	require.NoError(t, err)

	etag1 := fake.ETag(manifestKey)
	require.Equal(t, res1.ManifestETag, etag1)

	second := standardObjects(t, "b")
	second[1] = first[1] // the flag-set document keeps its bytes: it still names checkout-flags
	newFlagsKey := objectKey(t, second[0].Body)
	setsKey := objectKey(t, first[1].Body)

	fake.ResetCalls()

	res2, err := pub.Publish(t.Context(), s3publish.Request{Objects: second})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET " + manifestKey,
		"HEAD " + newFlagsKey, "PUT " + newFlagsKey + " create",
		"HEAD " + setsKey,
		"PUT " + manifestKey + " if-match=" + etag1,
	}, fake.Calls())
	assert.Equal(t, uint64(2), res2.Generation)
	assert.Equal(t, uint64(1), res2.Previous)
	assert.Equal(t, 1, res2.Uploaded)
	assert.Equal(t, 1, res2.Present)
	assert.Equal(t, int64(len(second[0].Body)), res2.UploadedBytes)
	assert.Equal(t, fake.ETag(manifestKey), res2.ManifestETag)
	assert.NotEqual(t, etag1, res2.ManifestETag)
	fake.AssertConsistent(t)
}

func TestPublishNothingChanged(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantCalls      func(flagsKey, setsKey, etag string) []string
		name           string
		generation     uint64
		wantGeneration uint64
		wantWritten    bool
	}{
		{
			name: "same content writes nothing, objects are checked",
			wantCalls: func(flagsKey, setsKey, _ string) []string {
				return []string{"GET " + manifestKey, "HEAD " + flagsKey, "HEAD " + setsKey}
			},
			wantGeneration: 1,
		},
		{
			name:       "same content and the same explicit generation writes nothing",
			generation: 1,
			wantCalls: func(flagsKey, setsKey, _ string) []string {
				return []string{"GET " + manifestKey, "HEAD " + flagsKey, "HEAD " + setsKey}
			},
			wantGeneration: 1,
		},
		{
			name:       "an explicit higher generation is written",
			generation: 7,
			wantCalls: func(flagsKey, setsKey, etag string) []string {
				return []string{
					"GET " + manifestKey,
					"HEAD " + flagsKey,
					"HEAD " + setsKey,
					"PUT " + manifestKey + " if-match=" + etag,
				}
			},
			wantGeneration: 7,
			wantWritten:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			pub := newPublisher(t, fake, nil)
			objects := standardObjects(t, "a")

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
			require.NoError(t, err)

			etag := fake.ETag(manifestKey)
			fake.ResetCalls()

			res, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects, Generation: tt.generation})
			require.NoError(t, err)

			assert.Equal(
				t,
				tt.wantCalls(objectKey(t, objects[0].Body), objectKey(t, objects[1].Body), etag),
				fake.Calls(),
			)
			assert.Equal(t, tt.wantGeneration, res.Generation)
			assert.Equal(t, tt.wantWritten, res.Written)
			assert.Equal(t, tt.wantWritten, res.Changed)
			assert.Equal(t, tt.wantWritten, fake.ETag(manifestKey) != etag)

			if !tt.wantWritten {
				assert.Equal(t, etag, res.ManifestETag)
				assert.Zero(t, res.Uploaded)

				assert.Equal(t, 2, res.Present, "the listed objects are confirmed present")

				for _, o := range res.Objects {
					assert.Equal(t, s3publish.StatusPresent, o.Status)
				}
			}
		})
	}
}

func TestPublishObjectAlreadyPresent(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	objects := standardObjects(t, "a")
	flagsKey := objectKey(t, objects[0].Body)
	setsKey := objectKey(t, objects[1].Body)
	fake.Seed(flagsKey, objects[0].Body)

	res, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET " + manifestKey,
		"HEAD " + flagsKey,
		"HEAD " + setsKey, "PUT " + setsKey + " create",
		"PUT " + manifestKey + " create",
	}, fake.Calls())
	assert.Equal(t, 1, res.Present)
	assert.Equal(t, 1, res.Uploaded)
	assert.Equal(t, s3publish.StatusPresent, res.Objects[0].Status)
}

func TestPublishSameContentTwiceUploadsOnce(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	body := flagsBody(t, "a")
	key := objectKey(t, body)
	objects := []s3publish.Object{
		{Name: nameOne, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
		{Name: nameTwo, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
	}

	res, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET " + manifestKey, "HEAD " + key, "PUT " + key + " create", "PUT " + manifestKey + " create",
	}, fake.Calls())
	assert.Equal(t, 1, res.Uploaded)
	assert.Equal(t, int64(len(body)), res.UploadedBytes)
	assert.Len(t, res.Objects, 2)
}

func TestPublishObjectCreatedByAnotherWriterAfterHead(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	objects := standardObjects(t, "a")
	flagsKey := objectKey(t, objects[0].Body)
	fake.Before = func(call string) error {
		if call == "PUT "+flagsKey+" create" {
			fake.Seed(flagsKey, objects[0].Body) // another publisher wrote the same content first
		}

		return nil
	}

	res, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Equal(t, s3publish.StatusPresent, res.Objects[0].Status)
	assert.Equal(t, 1, res.Uploaded)
	assert.Equal(t, uint64(1), res.Generation)
	assert.Equal(t, "HEAD "+flagsKey, fake.Calls()[3], "the key is checked again after the 412")
}

func TestPublishExistingObjectWithOtherContentIsNeverOverwritten(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	objects := standardObjects(t, "a")
	flagsKey := objectKey(t, objects[0].Body)
	fake.Seed(flagsKey, []byte("short"))

	_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.ErrorIs(t, err, s3publish.ErrObjectMismatch)

	assert.Equal(t, []string{"GET " + manifestKey, "HEAD " + flagsKey}, fake.Calls())

	got, _ := fake.Body(flagsKey)
	assert.Equal(t, []byte("short"), got)
	assert.Equal(t, []string{flagsKey}, fake.Keys())
}

func TestPublishLostManifestRace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		injectErr string
		hasFirst  bool
		deleteOld bool
		rival     bool
	}{
		{name: "first manifest, create-only write loses", rival: true},
		{name: "etag moved, 412", hasFirst: true, rival: true},
		{name: "ConditionalRequestConflict 409", hasFirst: true, injectErr: "ConditionalRequestConflict"},
		{name: "PreconditionFailed code", hasFirst: true, injectErr: "PreconditionFailed"},
		{name: "manifest deleted meanwhile", hasFirst: true, deleteOld: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			pub := newPublisher(t, fake, nil)

			if tt.hasFirst {
				_, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "old")})
				require.NoError(t, err)
			}

			oldETag := fake.ETag(manifestKey)
			oldBody, _ := fake.Body(manifestKey)

			fake.Before = func(call string) error {
				if !strings.HasPrefix(call, "PUT "+manifestKey) {
					return nil
				}

				switch {
				case tt.injectErr != "":
					return s3fake.APIError(tt.injectErr)
				case tt.deleteOld:
					fake.Remove(manifestKey)
				case tt.rival:
					fake.Seed(manifestKey, []byte("rival manifest"))
				}

				return nil
			}

			fake.ResetCalls()

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a")})
			require.ErrorIs(t, err, s3publish.ErrConflict)

			calls := fake.Calls()
			require.NotEmpty(t, calls)
			require.GreaterOrEqual(t, len(calls), 2)
			assert.True(
				t,
				strings.HasPrefix(calls[len(calls)-2], "PUT "+manifestKey),
				"the manifest write is the last write: %v",
				calls,
			)
			assert.Equal(t, "GET "+manifestKey, calls[len(calls)-1], "one read resolves the failed condition")
			assert.Equal(t, 1, countPrefix(calls, "PUT "+manifestKey), "no retry of the manifest write")

			got, found := fake.Body(manifestKey)

			switch {
			case tt.rival:
				assert.Equal(t, []byte("rival manifest"), got, "the winner's manifest is not clobbered")
			case tt.deleteOld:
				assert.False(t, found, "a lost race does not recreate the manifest")
			default:
				assert.Equal(t, oldETag, fake.ETag(manifestKey))
				assert.Equal(t, oldBody, got, "the manifest is unchanged")
			}
		})
	}
}

func countPrefix(calls []string, prefix string) int {
	total := 0

	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			total++
		}
	}

	return total
}

func TestPublishGenerationMustIncrease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		generation uint64
	}{
		{name: "lower", generation: 3},
		{name: "equal but other content", generation: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			pub := newPublisher(t, fake, nil)

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a"), Generation: 5})
			require.NoError(t, err)

			etag := fake.ETag(manifestKey)
			fake.ResetCalls()

			_, err = pub.Publish(
				t.Context(),
				s3publish.Request{Objects: standardObjects(t, "b"), Generation: tt.generation},
			)
			require.ErrorIs(t, err, s3publish.ErrGeneration)

			assert.Equal(t, []string{"GET " + manifestKey}, fake.Calls())
			assert.Equal(t, etag, fake.ETag(manifestKey))
		})
	}
}

func TestPublishValidationFailureWritesNothing(t *testing.T) {
	t.Parallel()

	good := standardObjects(t, "a")
	unresolved := flagSetBody(t, controlplane.FlagSet{Name: "x", Sources: []string{nameNope}})
	dupKey := flagSetBody(t,
		controlplane.FlagSet{Name: nameOne, Sources: []string{nameFlags}, APIKeys: []string{"same-key-canary"}},
		controlplane.FlagSet{Name: nameTwo, Sources: []string{nameFlags}, APIKeys: []string{"same-key-canary"}},
	)

	tests := []struct {
		name       string
		wantInErr  string
		objects    []s3publish.Object
		maxObjects int
	}{
		{
			name: "invalid name",
			objects: []s3publish.Object{
				{Name: badName, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: good[0].Body},
			},
			wantInErr: badName,
		},
		{
			name: "unknown kind",
			objects: []s3publish.Object{
				{Name: "a", Kind: otherName, Format: controlplane.FormatYAML, Body: good[0].Body},
			},
		},
		{
			name:    "toml is not published",
			objects: []s3publish.Object{{Name: "a", Kind: controlplane.KindFlags, Format: "toml", Body: good[0].Body}},
		},
		{name: "duplicate names", objects: []s3publish.Object{good[0], good[0]}, wantInErr: nameFlags},
		{
			name: "not yaml",
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: []byte("a: [")},
			},
		},
		{
			name: "flag without variations",
			objects: []s3publish.Object{
				{
					Name:   "a",
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Body:   []byte("f:\n  disable: true\n"),
				},
			},
		},
		{
			name: "unresolved source",
			objects: []s3publish.Object{
				good[0],
				{Name: "sets", Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML, Body: unresolved},
			},
		},
		{
			name: "duplicate api key",
			objects: []s3publish.Object{
				good[0],
				{Name: "sets", Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML, Body: dupKey},
			},
		},
		{name: msgTooMany, maxObjects: 1, objects: good},
		{
			name: "flag-set document as flags",
			objects: []s3publish.Object{
				{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: good[1].Body},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			pub := newPublisher(t, fake, func(c *s3publish.Config) { c.MaxObjects = tt.maxObjects })

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: tt.objects})
			require.ErrorIs(t, err, s3publish.ErrInvalid)

			assert.Empty(t, fake.Calls(), "validation failed: not one S3 call")
			assert.NotContains(t, err.Error(), "same-key-canary")

			if tt.wantInErr != "" {
				assert.Contains(t, err.Error(), tt.wantInErr)
			}
		})
	}
}

func TestPublishTransientErrorThenRetryConverges(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	pub := newPublisher(t, fake, nil)
	objects := standardObjects(t, "a")
	flagsKey := objectKey(t, objects[0].Body)
	setsKey := objectKey(t, objects[1].Body)

	var failed atomic.Bool

	fake.Before = func(call string) error {
		if call == "PUT "+setsKey+" create" && failed.CompareAndSwap(false, true) {
			return s3fake.APIError("ServiceUnavailable")
		}

		return nil
	}

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})

	var op *s3publish.OperationError

	require.ErrorAs(t, err, &op)
	assert.Equal(t, "PutObject", op.Operation)
	assert.Equal(t, setsKey, op.Key)
	assert.Equal(t, []string{flagsKey}, fake.Keys(), "only the first object exists; no manifest")

	fake.ResetCalls()

	res, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET " + manifestKey,
		"HEAD " + flagsKey,
		"HEAD " + setsKey, "PUT " + setsKey + " create",
		"PUT " + manifestKey + " create",
	}, fake.Calls())
	assert.Equal(t, uint64(1), res.Generation)
	assert.Equal(t, 1, res.Present)
	assert.Equal(t, 1, res.Uploaded)
	fake.AssertConsistent(t)
}

// lostResponse performs the manifest write and then reports an error, as a dropped connection does.
type lostResponse struct{ *s3fake.Fake }

func (l lostResponse) PutObject(
	ctx context.Context,
	in *s3.PutObjectInput,
	opts ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	out, err := l.Fake.PutObject(ctx, in, opts...)
	if err == nil && aws.ToString(in.Key) == manifestKey {
		return nil, s3fake.APIError("RequestTimeout")
	}

	return out, err
}

func TestPublishLostResponseOfManifestWriteIsSafeToRepeat(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	objects := standardObjects(t, "a")

	_, err := newPublisher(t, lostResponse{fake}, nil).Publish(t.Context(), s3publish.Request{Objects: objects})

	var op *s3publish.OperationError

	require.ErrorAs(t, err, &op)
	require.NotEmpty(t, fake.ETag(manifestKey), "the write landed")

	again, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.False(t, again.Written, "the repeat finds its own manifest and writes nothing")
	assert.Equal(t, uint64(1), again.Generation)
}

func TestPublishDryRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		wantStatus  s3publish.Status
		wantCount   int
		seedObjects bool
	}{
		{name: "absent objects would be uploaded", wantStatus: s3publish.StatusWouldUpload, wantCount: 2},
		{
			name:        "present objects are reported present",
			seedObjects: true,
			wantStatus:  s3publish.StatusPresent,
			wantCount:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			objects := standardObjects(t, "a")

			if tt.seedObjects {
				for _, o := range objects {
					fake.Seed(objectKey(t, o.Body), o.Body)
				}
			}

			before := fake.Keys()

			res, err := newPublisher(
				t,
				fake,
				nil,
			).Publish(t.Context(), s3publish.Request{Objects: objects, DryRun: true})
			require.NoError(t, err)

			for _, c := range fake.Calls() {
				assert.NotContains(t, c, "PUT", "a dry run makes no write")
			}

			assert.Equal(t, before, fake.Keys())
			assert.True(t, res.DryRun)
			assert.True(t, res.Changed)
			assert.False(t, res.Written)
			assert.Equal(t, uint64(1), res.Generation)
			assert.Equal(t, tt.wantCount, res.Uploaded)

			for _, o := range res.Objects {
				assert.Equal(t, tt.wantStatus, o.Status)
			}
		})
	}
}

func TestPublishManifestIsAlwaysTheLastCallAndNothingListsOrDeletes(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	pub := newPublisher(t, fake, nil)

	for _, label := range []string{"a", "b", "c"} {
		fake.ResetCalls()

		_, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, label)})
		require.NoError(t, err)

		calls := fake.Calls()
		assert.Equal(t, 1, countPrefix(calls, "PUT "+manifestKey))
		assert.True(
			t,
			strings.HasPrefix(calls[len(calls)-1], "PUT "+manifestKey),
			"the manifest write is the last call: %v",
			calls,
		)

		for _, c := range calls {
			assert.Regexp(t, `^(GET|HEAD|PUT) `, c, "only GET, HEAD and PUT exist in the API")
		}
	}

	fake.AssertConsistent(t)
	assert.Len(t, fake.Keys(), 1+2*3-2, "old objects are never deleted: manifest + 4 flag/flag-set objects")
}

func TestPublishReadErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup   func(f *s3fake.Fake)
		wantErr error
		name    string
		wantOp  bool
	}{
		{name: "transport error reading the manifest", setup: func(f *s3fake.Fake) {
			f.Before = func(call string) error {
				if call == "GET "+manifestKey {
					return s3fake.APIError("InternalError")
				}

				return nil
			}
		}, wantOp: true},
		{
			name:    "invalid manifest in the bucket",
			setup:   func(f *s3fake.Fake) { f.Seed(manifestKey, []byte("not: a manifest")) },
			wantErr: s3publish.ErrPreviousManifest,
		},
		{name: "other channel", setup: func(f *s3fake.Fake) {
			m, err := controlplane.MarshalManifest(&controlplane.Manifest{Channel: "canary", Generation: 4})
			if err == nil {
				f.Seed(manifestKey, m)
			}
		}, wantErr: s3publish.ErrChannelMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			tt.setup(fake)

			_, err := newPublisher(
				t,
				fake,
				nil,
			).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a")})
			require.Error(t, err)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}

			var op *s3publish.OperationError

			assert.Equal(t, tt.wantOp, errors.As(err, &op))
			assert.Equal(t, []string{"GET " + manifestKey}, fake.Calls(), "nothing else is called")
		})
	}
}

func TestPublishWarnsAboutUnknownFields(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	body := []byte("flag-a:\n  variations: {on: true, off: false}\n  defaultRule: {variation: off}\n  futureField: 1\n")
	objects := []s3publish.Object{
		{Name: "a", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: body},
	}

	res, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Len(t, res.Warnings, 1)
	assert.Contains(t, res.Warnings[0], "object a: unknown field")
	assert.Contains(t, res.Warnings[0], "futureField")
}

func TestNewRejectsNilConfig(t *testing.T) {
	t.Parallel()

	pub, err := s3publish.New(s3fake.New(manifestKey), nil)
	require.ErrorIs(t, err, s3publish.ErrConfig)
	assert.Nil(t, pub)
}
