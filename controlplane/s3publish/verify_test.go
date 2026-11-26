package s3publish_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

func publishStandard(t *testing.T, fake *s3fake.Fake) (*s3publish.Publisher, []s3publish.Object) {
	t.Helper()

	pub := newPublisher(t, fake, nil)
	objects := standardObjects(t, "a")

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	fake.ResetCalls()

	return pub, objects
}

func TestVerifyHealthyBucket(t *testing.T) {
	t.Parallel()

	fake := newFake()
	pub, objects := publishStandard(t, fake)

	rep, err := pub.Verify(t.Context())
	require.NoError(t, err)

	assert.True(t, rep.OK())
	assert.Empty(t, rep.Problems)
	assert.Equal(t, 2, rep.Checked)
	assert.Equal(t, s3publish.Requests{Get: 3}, rep.Requests)
	assert.Equal(t, uint64(1), rep.Generation)
	assert.Equal(t, fake.ETag(manifestKey), rep.ManifestETag)
	assert.Equal(t, []string{
		"GET " + manifestKey,
		"GET " + objectKey(t, objects[0].Body),
		"GET " + objectKey(t, objects[1].Body),
	}, fake.Calls(), "verify only reads")
}

func TestVerifyReportsEveryProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		damage      func(f *s3fake.Fake, flagsKey string)
		wantCode    string
		wantObject  string
		wantChecked int
	}{
		{
			name:       "missing object",
			damage:     func(f *s3fake.Fake, k string) { f.Remove(k) },
			wantCode:   s3publish.ProblemMissing,
			wantObject: nameFlags,
		},
		{
			name:       "corrupted object of the same size",
			wantCode:   s3publish.ProblemChecksumMismatch,
			wantObject: nameFlags,
			damage: func(f *s3fake.Fake, k string) {
				b, _ := f.Body(k)
				b[len(b)-2] ^= 0x01
				f.Seed(k, b)
			},
		},
		{
			name:       "truncated object",
			damage:     func(f *s3fake.Fake, k string) { b, _ := f.Body(k); f.Seed(k, b[:len(b)-3]) },
			wantCode:   s3publish.ProblemSizeMismatch,
			wantObject: nameFlags,
		},
		{
			name:       "longer object",
			damage:     func(f *s3fake.Fake, k string) { b, _ := f.Body(k); f.Seed(k, append(b, "padding"...)) },
			wantCode:   s3publish.ProblemSizeMismatch,
			wantObject: nameFlags,
		},
		{
			name: "unreadable object", wantCode: s3publish.ProblemUnreadable, wantObject: nameFlags,
			damage: func(f *s3fake.Fake, k string) {
				f.Before = func(call string) error {
					if call == "GET "+k {
						return s3fake.APIError("AccessDenied")
					}

					return nil
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			pub, objects := publishStandard(t, fake)
			tt.damage(fake, objectKey(t, objects[0].Body))

			rep, err := pub.Verify(t.Context())
			require.NoError(t, err)

			assert.False(t, rep.OK())
			require.Len(t, rep.Problems, 1)
			assert.Equal(t, tt.wantCode, rep.Problems[0].Code)
			assert.Equal(t, tt.wantObject, rep.Problems[0].Object)
			assert.Equal(t, objectKey(t, objects[0].Body), rep.Problems[0].Key)
			assert.Equal(t, 2, rep.Checked, "the healthy object is still checked")
		})
	}
}

func TestVerifyUnresolvedSourceAndInvalidContent(t *testing.T) {
	t.Parallel()

	fake := newFake()
	flags := flagsBody(t, "a")
	sets := flagSetBody(
		t,
		controlplane.FlagSet{Name: "x", Sources: []string{nameNope}, APIKeys: []string{"canary-api-key-0002"}},
	)
	garbage := []byte("flag: [unterminated")

	manifest := &controlplane.Manifest{Channel: testChannel, Generation: 3}

	for name, obj := range map[string]struct {
		kind string
		body []byte
	}{
		nameFlags: {controlplane.KindFlags, flags},
		nameSets:  {controlplane.KindFlagSets, sets},
		"garbage": {controlplane.KindFlags, garbage},
	} {
		key := objectKey(t, obj.body)
		fake.Seed(key, obj.body)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: name, Kind: obj.kind, Connection: testConnection, Key: key, Format: controlplane.FormatYAML,
			Checksum: controlplane.ChecksumOf(obj.body), Size: int64(len(obj.body)),
		})
	}

	body, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)
	fake.Seed(manifestKey, body)

	rep, err := newPublisher(t, fake, nil).Verify(t.Context())
	require.NoError(t, err)

	got := map[string]string{}
	for _, p := range rep.Problems {
		got[p.Object] = p.Code
		assert.NotContains(t, p.Detail, "canary-api-key-0002")
	}

	assert.Equal(t, map[string]string{
		nameSets:  string(controlplane.CodeUnresolvedSource),
		"garbage": s3publish.ProblemInvalidContent,
	}, got)
	assert.Equal(t, 3, rep.Checked)
}

func TestVerifyManifestStates(t *testing.T) {
	t.Parallel()

	t.Run("no manifest", func(t *testing.T) {
		t.Parallel()

		rep, err := newPublisher(t, newFake(), nil).Verify(t.Context())
		require.ErrorIs(t, err, s3publish.ErrNoManifest)
		assert.Nil(t, rep)
	})

	t.Run("invalid manifest is a problem", func(t *testing.T) {
		t.Parallel()

		fake := newFake()
		fake.Seed(manifestKey, []byte("apiVersion: semaphore/v1\nkind: FlagSet\nflagSets: []\n"))

		rep, err := newPublisher(t, fake, nil).Verify(t.Context())
		require.NoError(t, err)
		require.Len(t, rep.Problems, 1)
		assert.Equal(t, s3publish.ProblemManifestInvalid, rep.Problems[0].Code)
		assert.Equal(t, 0, rep.Checked)
	})

	t.Run("read error is an OperationError", func(t *testing.T) {
		t.Parallel()

		fake := newFake()
		fake.Before = func(string) error { return s3fake.APIError("InternalError") }

		_, err := newPublisher(t, fake, nil).Verify(t.Context())

		var op *s3publish.OperationError

		require.ErrorAs(t, err, &op)
		assert.Equal(t, "GetObject", op.Operation)
	})

	t.Run("other channel", func(t *testing.T) {
		t.Parallel()

		fake := newFake()
		body, err := controlplane.MarshalManifest(&controlplane.Manifest{Channel: "canary", Generation: 1})
		require.NoError(t, err)
		fake.Seed(manifestKey, body)

		rep, err := newPublisher(t, fake, nil).Verify(t.Context())
		require.NoError(t, err)
		require.Len(t, rep.Problems, 1)
		assert.Equal(t, s3publish.ProblemManifestInvalid, rep.Problems[0].Code)
	})

	t.Run("objects of other connections are skipped", func(t *testing.T) {
		t.Parallel()

		fake := newFake()
		body, err := controlplane.MarshalManifest(&controlplane.Manifest{
			Channel: testChannel, Generation: 1,
			Objects: []controlplane.ManifestObject{
				{
					Name:       "elsewhere",
					Kind:       controlplane.KindFlags,
					Connection: otherName,
					Key:        "k",
					Format:     controlplane.FormatYAML,
					Checksum:   controlplane.ChecksumOf([]byte("x")),
					Size:       1,
				},
			},
		})
		require.NoError(t, err)
		fake.Seed(manifestKey, body)

		rep, err := newPublisher(t, fake, nil).Verify(t.Context())
		require.NoError(t, err)
		assert.True(t, rep.OK())
		assert.Equal(t, []string{"elsewhere"}, rep.Skipped)
		assert.Equal(t, 0, rep.Checked)
		assert.Equal(t, []string{"GET " + manifestKey}, fake.Calls())
	})
}
