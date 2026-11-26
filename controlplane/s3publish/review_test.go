package s3publish_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

func TestManifestKeyThatIsAnObjectKeyIsRefused(t *testing.T) {
	t.Parallel()

	objects := standardObjects(t, "a")
	collide := objectKey(t, objects[0].Body)

	tests := []struct {
		name string
		req  s3publish.Request
		seed bool
	}{
		{name: "first publish", req: s3publish.Request{Objects: objects}},
		{name: "recovery of an invalid manifest", seed: true, req: s3publish.Request{
			Objects: objects, ReplaceInvalidManifest: true, Generation: 1,
		}},
		{name: nameDryRun, req: s3publish.Request{Objects: objects, DryRun: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(collide)
			if tt.seed {
				fake.Seed(collide, []byte("not a manifest"))
			}

			cfg := testConfig()
			cfg.ManifestKey = collide

			pub, err := s3publish.New(fake, &cfg)
			require.NoError(t, err)

			_, err = pub.Publish(t.Context(), tt.req)
			require.ErrorIs(t, err, s3publish.ErrManifestKey)
			require.ErrorIs(t, err, s3publish.ErrInvalid)
			assert.Contains(t, err.Error(), "checkout-flags")
			assert.Empty(t, fake.Calls(), "refused before any S3 call")
		})
	}
}

func TestPublishRefusesToWrapTheGeneration(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	seedBucket(t, fake, math.MaxUint64, standardObjects(t, "a")[0])
	pub := newPublisher(t, fake, nil)

	t.Run("changed content has no next generation", func(t *testing.T) {
		t.Parallel()

		_, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "b")[:1], DryRun: true})
		require.ErrorIs(t, err, s3publish.ErrGeneration)
	})

	t.Run("unchanged content needs none", func(t *testing.T) {
		t.Parallel()

		res, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a")[:1]})
		require.NoError(t, err)
		assert.Equal(t, uint64(math.MaxUint64), res.Generation)
		assert.False(t, res.Written)
	})
}

// compositeHead reports a composite checksum type with a bare digest (which says nothing about the bytes).
type compositeHead struct {
	s3publish.API

	digest string
}

func (c compositeHead) HeadObject(
	ctx context.Context, input *s3.HeadObjectInput, opts ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	out, err := c.API.HeadObject(ctx, input, opts...)
	if err == nil {
		out.ChecksumSHA256 = aws.String(c.digest)
		out.ChecksumType = types.ChecksumTypeComposite
	}

	return out, err
}

func TestACompositeChecksumTypeIsNeverTrustedEvenWithoutASuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		corrupt bool
	}{
		{name: "intact bytes are hashed and accepted"},
		{name: "same-size other bytes are found by the hash", corrupt: true, wantErr: s3publish.ErrObjectMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			objects := standardObjects(t, "a")[:1]
			key := objectKey(t, objects[0].Body)

			stored := append([]byte{}, objects[0].Body...)
			if tt.corrupt {
				stored[0] ^= 0x01
			}

			fake.Seed(key, stored)

			// The digest is the one that the publisher expects, so a client that trusted it would accept corrupt bytes.
			expected := aws.ToString(newSHA(objects[0].Body))

			_, err := newPublisher(t, compositeHead{API: fake, digest: expected}, nil).
				Publish(t.Context(), s3publish.Request{Objects: objects})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Contains(t, fake.Calls(), "GET "+key, "the bytes were read and hashed")
		})
	}
}

func TestVerifyReadsThePinnedVersion(t *testing.T) {
	t.Parallel()

	body := flagsBody(t, "a")
	key := objectKey(t, body)

	manifestWith := func(t *testing.T, versionID string) []byte {
		t.Helper()

		out, err := controlplane.MarshalManifest(&controlplane.Manifest{
			Channel: testChannel, Generation: 2,
			Objects: []controlplane.ManifestObject{{
				Name: "pinned", Kind: controlplane.KindFlags, Connection: testConnection, Key: key,
				Format: controlplane.FormatYAML, Checksum: controlplane.ChecksumOf(body), Size: int64(len(body)),
				VersionID: versionID,
			}},
		})
		require.NoError(t, err)

		return out
	}

	tests := []struct {
		versions  map[string][]byte
		name      string
		versionID string
		wantCode  string
		wantGet   string
		latest    []byte
	}{
		{
			name: "the pinned version is intact although the latest is another", versionID: "v1",
			latest: []byte("a newer and different object"), versions: map[string][]byte{"v1": body},
			wantGet: "GET " + key + " version=v1",
		},
		{
			name: "the pinned version is corrupt although the latest is intact", versionID: "v1",
			latest: body, versions: map[string][]byte{"v1": append([]byte("x"), body[1:]...)},
			wantCode: s3publish.ProblemChecksumMismatch, wantGet: "GET " + key + " version=v1",
		},
		{
			name: "an absent pinned version is missing", versionID: "gone", latest: body,
			wantCode: s3publish.ProblemMissing, wantGet: "GET " + key + " version=gone",
		},
		{name: "no pin reads the latest", latest: body, wantGet: "GET " + key},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			fake.Seed(key, tt.latest)

			for id, data := range tt.versions {
				fake.SeedVersion(key, id, data)
			}

			fake.Seed(manifestKey, manifestWith(t, tt.versionID))

			rep, err := newPublisher(t, fake, nil).Verify(t.Context())
			require.NoError(t, err)
			assert.Contains(t, fake.Calls(), tt.wantGet)

			if tt.wantCode == "" {
				assert.True(t, rep.OK(), "%v", rep.Problems)

				return
			}

			require.Len(t, rep.Problems, 1)
			assert.Equal(t, tt.wantCode, rep.Problems[0].Code)
		})
	}
}

func TestVerifyTreatsInvalidArgumentOnAPinnedVersionAsMissing(t *testing.T) {
	t.Parallel()

	body := flagsBody(t, "a")
	key := objectKey(t, body)
	fake := s3fake.New(manifestKey)

	manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
		Channel: testChannel, Generation: 2,
		Objects: []controlplane.ManifestObject{
			{
				Name:       "pinned",
				Kind:       controlplane.KindFlags,
				Connection: testConnection,
				Key:        key,
				Format:     controlplane.FormatYAML,
				Checksum:   controlplane.ChecksumOf(body),
				Size:       int64(len(body)),
				VersionID:  "bogus",
			},
		},
	})
	require.NoError(t, err)

	fake.Seed(manifestKey, manifest)
	fake.Seed(key, body)

	// AWS answers 400 InvalidArgument for a version id that does not exist (the emulator answers 404).
	fake.Before = func(call string) error {
		if call == "GET "+key+" version=bogus" {
			return &s3fake.StatusError{Code: "InvalidArgument", Status: 400}
		}

		return nil
	}

	rep, err := newPublisher(t, fake, nil).Verify(t.Context())
	require.NoError(t, err)
	require.Len(t, rep.Problems, 1)
	assert.Equal(t, s3publish.ProblemMissing, rep.Problems[0].Code)
}

var errEngine = errors.New("flag f: query does not compile")

func TestVerifyRunsTheCheckHook(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	seedBucket(t, fake, 3, standardObjects(t, "a")...)

	var seen []string

	pub := newPublisher(t, fake, func(c *s3publish.Config) {
		c.Check = func(obj *s3publish.Object) error {
			seen = append(seen, obj.Name+"|"+obj.Kind+"|"+obj.Format)

			if obj.Kind == controlplane.KindFlags {
				return errEngine
			}

			return nil
		}
	})

	rep, err := pub.Verify(t.Context())
	require.NoError(t, err)

	require.Len(t, rep.Problems, 1)
	assert.Equal(t, nameFlags, rep.Problems[0].Object)
	assert.Equal(t, s3publish.ProblemInvalidContent, rep.Problems[0].Code)
	assert.Contains(t, rep.Problems[0].Detail, "query does not compile")
	assert.ElementsMatch(t, []string{"checkout-flags|flags|yaml", "flagsets|flagSets|yaml"}, seen)
}

func TestVerifyDoesNotCallTheHookForAnObjectThatIsAlreadyBad(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	objects := standardObjects(t, "a")
	seedBucket(t, fake, 3, objects...)
	fake.Remove(objectKey(t, objects[0].Body))

	called := false
	pub := newPublisher(t, fake, func(c *s3publish.Config) {
		c.Check = func(obj *s3publish.Object) error {
			called = called || obj.Kind == controlplane.KindFlags

			return nil
		}
	})

	rep, err := pub.Verify(t.Context())
	require.NoError(t, err)
	require.Len(t, rep.Problems, 1)
	assert.Equal(t, s3publish.ProblemMissing, rep.Problems[0].Code)
	assert.False(t, called)
}

func TestVerifyAcceptsTOMLFlagFilesTheRelayCanLoad(t *testing.T) {
	t.Parallel()

	toml := []byte("[flag-a]\n[flag-a.variations]\non = true\noff = false\n[flag-a.defaultRule]\nvariation = \"off\"\n")
	key, err := controlplane.ObjectKey(controlplane.ChecksumOf(toml), controlplane.FormatTOML)
	require.NoError(t, err)

	manifest, err := controlplane.MarshalManifest(&controlplane.Manifest{
		Channel: testChannel, Generation: 1,
		Objects: []controlplane.ManifestObject{{
			Name: "toml-flags", Kind: controlplane.KindFlags, Connection: testConnection, Key: key,
			Format: controlplane.FormatTOML, Checksum: controlplane.ChecksumOf(toml), Size: int64(len(toml)),
		}},
	})
	require.NoError(t, err)

	fake := s3fake.New(manifestKey)
	fake.Seed(key, toml)
	fake.Seed(manifestKey, manifest)

	var checked []string

	rep, err := newPublisher(t, fake, func(c *s3publish.Config) {
		c.Check = func(obj *s3publish.Object) error {
			checked = append(checked, obj.Format)

			return nil
		}
	}).Verify(t.Context())

	require.NoError(t, err)
	assert.True(t, rep.OK(), "valid TOML is not invalid content: %v", rep.Problems)
	assert.Equal(t, []string{controlplane.FormatTOML}, checked, "the hook (the engine) still looks at it")
}

func TestAccessDeniedIsAStoreErrorWithAHintAndNeverMeansExistsOrMissing(t *testing.T) {
	t.Parallel()

	denied := &s3fake.StatusError{Code: "AccessDenied", Status: 403}
	hint := "lacks s3:ListBucket"

	tests := []struct {
		check func(t *testing.T, err error, fake *s3fake.Fake)
		name  string
		deny  string // the call prefix that answers 403
	}{
		{
			name: "the manifest read: a first publish does not guess that there is none", deny: "GET " + manifestKey,
			check: func(t *testing.T, err error, fake *s3fake.Fake) {
				t.Helper()
				require.ErrorContains(t, err, hint)
				assert.Equal(t, []string{"GET " + manifestKey}, fake.Calls(), "nothing is written")
			},
		},
		{
			name: "the check of a new object is not taken for an existing one", deny: "HEAD ",
			check: func(t *testing.T, err error, fake *s3fake.Fake) {
				t.Helper()
				require.ErrorContains(t, err, hint)
				assert.NotContains(t, strings.Join(fake.Calls(), "\n"), "PUT ")
				assert.Empty(t, fake.Keys())
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			failOn(fake, tt.deny, denied)

			_, err := newPublisher(
				t,
				fake,
				nil,
			).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a")})

			var op *s3publish.OperationError

			require.ErrorAs(t, err, &op)
			require.ErrorIs(t, err, denied)
			require.NotErrorIs(t, err, s3publish.ErrNoManifest)
			tt.check(t, err, fake)
		})
	}

	t.Run("other errors carry no hint", func(t *testing.T) {
		t.Parallel()

		fake := s3fake.New(manifestKey)
		failOn(fake, "GET ", s3fake.APIError("ServiceUnavailable"))

		_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a")})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), hint)
	})

	t.Run("verify reports the object as unreadable with the hint", func(t *testing.T) {
		t.Parallel()

		fake := s3fake.New(manifestKey)
		objects := standardObjects(t, "a")
		seedBucket(t, fake, 1, objects...)

		key := objectKey(t, objects[0].Body)
		failOn(fake, "GET "+key, denied)

		rep, err := newPublisher(t, fake, nil).Verify(t.Context())
		require.NoError(t, err)
		require.Len(t, rep.Problems, 1)
		assert.Equal(t, s3publish.ProblemUnreadable, rep.Problems[0].Code)
		assert.Contains(t, rep.Problems[0].Detail, hint)
	})
}
