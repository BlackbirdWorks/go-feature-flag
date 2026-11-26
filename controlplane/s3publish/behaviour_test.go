package s3publish_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

const canaryDocKey = "canary-doc-key-a1b2c3"

func TestPublishValidatesFlagSetDocumentsTogether(t *testing.T) {
	t.Parallel()

	flags := s3publish.Object{
		Name:   nameFlags,
		Kind:   controlplane.KindFlags,
		Format: controlplane.FormatYAML,
		Source: "flags/f.yaml",
		Body:   flagsBody(t, "a"),
	}
	setsDoc := func(name, file string, sets ...controlplane.FlagSet) s3publish.Object {
		return s3publish.Object{
			Name:   name,
			Kind:   controlplane.KindFlagSets,
			Format: controlplane.FormatYAML,
			Source: file,
			Body:   flagSetBody(t, sets...),
		}
	}
	setOf := func(name, key string) controlplane.FlagSet {
		return controlplane.FlagSet{Name: name, Sources: []string{nameFlags}, APIKeys: []string{key}}
	}

	tests := []struct {
		name      string
		objects   []s3publish.Object
		wantInErr []string
	}{
		{
			name: "one flag-set name in two documents",
			objects: []s3publish.Object{
				flags,
				setsDoc("sets-a", "flagsets/a.yaml", setOf("shop", "k1")),
				setsDoc("sets-b", "flagsets/b.yaml", setOf("shop", "k2")),
			},
			wantInErr: []string{
				"documents[1].flagSets[0].name",
				"0=sets-a (flagsets/a.yaml)",
				"1=sets-b (flagsets/b.yaml)",
			},
		},
		{
			name: "one API key in two documents is reported by position, never by text",
			objects: []s3publish.Object{
				flags,
				setsDoc("sets-a", "flagsets/a.yaml", setOf(nameOne, canaryDocKey)),
				setsDoc("sets-b", "flagsets/b.yaml", setOf(nameTwo, canaryDocKey)),
			},
			wantInErr: []string{
				"documents[1].flagSets[0].apiKeys[0]",
				"duplicate of documents[0].flagSets[0].apiKeys[0]",
			},
		},
		{
			name: "a source that no flags object provides",
			objects: []s3publish.Object{
				flags,
				setsDoc("sets-a", "flagsets/a.yaml", controlplane.FlagSet{Name: nameOne, Sources: []string{nameNope}}),
			},
			wantInErr: []string{"documents[0].flagSets[0].sources[0]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)

			_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: tt.objects})
			require.ErrorIs(t, err, s3publish.ErrInvalid)
			assert.Empty(t, fake.Calls())
			assert.NotContains(t, err.Error(), canaryDocKey)

			for _, want := range tt.wantInErr {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// seedBucket stores the objects under their content-addressed keys and a manifest that lists them.
func seedBucket(t *testing.T, fake *s3fake.Fake, generation uint64, objects ...s3publish.Object) {
	t.Helper()

	manifest := &controlplane.Manifest{Channel: testChannel, Generation: generation}

	for i := range objects {
		key := objectKey(t, objects[i].Body)
		fake.Seed(key, objects[i].Body)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name:       objects[i].Name,
			Kind:       objects[i].Kind,
			Connection: testConnection,
			Key:        key,
			Format:     objects[i].Format,
			Checksum:   controlplane.ChecksumOf(objects[i].Body),
			Size:       int64(len(objects[i].Body)),
		})
	}

	body, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)
	fake.Seed(manifestKey, body)
}

func TestVerifyValidatesFlagSetDocumentsTogether(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	docOf := func(name string, sets ...controlplane.FlagSet) s3publish.Object {
		return s3publish.Object{
			Name:   name,
			Kind:   controlplane.KindFlagSets,
			Format: controlplane.FormatYAML,
			Body:   flagSetBody(t, sets...),
		}
	}
	shop := func(extra string, key string) []controlplane.FlagSet {
		return []controlplane.FlagSet{
			{Name: "shop-" + extra, Sources: []string{nameFlags}, APIKeys: []string{key}},
		}
	}

	seedBucket(
		t,
		fake,
		3,
		s3publish.Object{
			Name:   nameFlags,
			Kind:   controlplane.KindFlags,
			Format: controlplane.FormatYAML,
			Body:   flagsBody(t, "a"),
		},
		docOf("sets-a", shop("a", canaryDocKey)...),
		docOf("sets-b", shop("b", canaryDocKey)...),
	)

	rep, err := newPublisher(t, fake, nil).Verify(t.Context())
	require.NoError(t, err)
	require.Len(t, rep.Problems, 1)
	assert.Equal(t, "sets-b", rep.Problems[0].Object, "the later document has the problem")
	assert.Equal(t, string(controlplane.CodeDuplicateKey), rep.Problems[0].Code)
	assert.NotContains(t, rep.Problems[0].Detail, canaryDocKey)
}

var errBadFlag = errors.New("flag checkout-new: query does not compile")

func TestPublishCheckHook(t *testing.T) {
	t.Parallel()

	tests := []struct {
		check     func(*s3publish.Object) error
		name      string
		wantCalls int
		wantErr   bool
	}{
		{
			name:      "every object is offered and none refuses",
			check:     func(*s3publish.Object) error { return nil },
			wantCalls: 2,
		},
		{
			name: "a refusal names the file, the hook's reason and writes nothing",
			check: func(obj *s3publish.Object) error {
				if obj.Kind == controlplane.KindFlags {
					return errBadFlag
				}

				return nil
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var offered []string

			fake := s3fake.New(manifestKey)
			pub := newPublisher(t, fake, func(c *s3publish.Config) {
				c.Check = func(obj *s3publish.Object) error {
					offered = append(offered, obj.Name+"|"+obj.Source+"|"+obj.Kind)

					return tt.check(obj)
				}
			})

			objects := standardObjects(t, "a")
			objects[0].Source = "flags/checkout.yaml"

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})

			if tt.wantErr {
				require.ErrorIs(t, err, s3publish.ErrInvalid)
				require.ErrorIs(t, err, errBadFlag)
				assert.Contains(t, err.Error(), "checkout-flags (flags/checkout.yaml)")
				assert.Contains(t, err.Error(), "checkout-new")
				assert.Empty(t, fake.Calls(), "a refused check writes nothing and reads nothing")

				return
			}

			require.NoError(t, err)
			assert.Len(t, offered, tt.wantCalls)
			assert.Contains(t, offered, "checkout-flags|flags/checkout.yaml|flags")
		})
	}
}

func TestMissingBucketIsAnErrorNotAMissingManifestNorAConflict(t *testing.T) {
	t.Parallel()

	noBucket := &s3fake.StatusError{Code: "NoSuchBucket", Status: 404}

	tests := []struct {
		run      func(pub *s3publish.Publisher, objects []s3publish.Object) error
		setup    func(fake *s3fake.Fake)
		name     string
		hasOlder bool // an older manifest exists, so the manifest write is If-Match
	}{
		{
			name: "publish",
			run: func(pub *s3publish.Publisher, objects []s3publish.Object) error {
				_, err := pub.Publish(context.Background(), s3publish.Request{Objects: objects})

				return err
			},
		},
		{
			name: nameDryRun,
			run: func(pub *s3publish.Publisher, objects []s3publish.Object) error {
				_, err := pub.Publish(context.Background(), s3publish.Request{Objects: objects, DryRun: true})

				return err
			},
		},
		{
			name: "verify",
			run: func(pub *s3publish.Publisher, _ []s3publish.Object) error {
				_, err := pub.Verify(context.Background())

				return err
			},
		},
		{
			name:     "the bucket vanishes before the manifest write",
			hasOlder: true,
			setup:    func(fake *s3fake.Fake) { failOn(fake, "PUT "+manifestKey, noBucket) },
			run: func(pub *s3publish.Publisher, objects []s3publish.Object) error {
				_, err := pub.Publish(context.Background(), s3publish.Request{Objects: objects})

				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			objects := standardObjects(t, "a")

			if tt.hasOlder {
				// An older manifest exists, so the write is If-Match: a 404 there used to mean "deleted".
				_, err := newPublisher(
					t,
					fake,
					nil,
				).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "old")})
				require.NoError(t, err)
			} else {
				failOn(fake, "", noBucket)
			}

			if tt.setup != nil {
				tt.setup(fake)
			}

			err := tt.run(newPublisher(t, fake, nil), objects)

			var op *s3publish.OperationError

			require.ErrorAs(t, err, &op)
			require.ErrorIs(t, err, noBucket)
			require.NotErrorIs(t, err, s3publish.ErrConflict)
			require.NotErrorIs(t, err, s3publish.ErrNoManifest)
		})
	}
}

// failOn makes the calls that start with prefix fail with err (every call for an empty prefix).
func failOn(fake *s3fake.Fake, prefix string, err error) {
	fake.Before = func(call string) error {
		if strings.HasPrefix(call, prefix) {
			return err
		}

		return nil
	}
}

// retrying simulates the retry of the SDK: a manifest write is committed and its answer is lost; the SDK then
// sends the same request again, which fails its If-Match / If-None-Match condition.
type retrying struct {
	s3publish.API

	retried atomic.Int32
}

func (r *retrying) PutObject(
	ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	out, err := r.API.PutObject(ctx, input, opts...)
	if err != nil || aws.ToString(input.Key) != manifestKey || !r.retried.CompareAndSwap(0, 1) {
		return out, err
	}

	// The SDK rewinds a seekable body before it sends a request again.
	if seeker, ok := input.Body.(io.Seeker); ok {
		_, seekErr := seeker.Seek(0, io.SeekStart)
		if seekErr != nil {
			return nil, fmt.Errorf("rewind: %w", seekErr)
		}
	}

	return r.API.PutObject(ctx, input, opts...)
}

func TestManifestWriteThatCommittedBeforeTheRetryIsSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		first bool
	}{
		{name: "first manifest, retried create-only write", first: true},
		{name: "next generation, retried If-Match write"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			if !tt.first {
				_, err := newPublisher(
					t,
					fake,
					nil,
				).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "old")})
				require.NoError(t, err)
			}

			api := &retrying{API: fake}

			res, err := newPublisher(
				t,
				api,
				nil,
			).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "new")})
			require.NoError(t, err, "our own earlier attempt committed: that is success")

			assert.Equal(t, int32(1), api.retried.Load())
			assert.True(t, res.Written)
			assert.Equal(t, fake.ETag(manifestKey), res.ManifestETag, "the ETag of what is in the bucket")

			stored, ok := fake.Body(manifestKey)
			require.True(t, ok)

			manifest, _, err := controlplane.UnmarshalManifest(stored)
			require.NoError(t, err)
			assert.Equal(t, res.Generation, manifest.Generation)
		})
	}
}

func TestManifestWriteWithAnotherWinnerIsStillAConflict(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	seedBucket(t, fake, 4, s3publish.Object{
		Name: nameRival, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flagsBody(t, nameRival),
	})

	// Another publisher wrote generation 5 with other content between our read and our write.
	fake.Before = func(call string) error {
		if strings.HasPrefix(call, "PUT "+manifestKey) {
			seedBucket(t, fake, 5, s3publish.Object{
				Name:   nameRival,
				Kind:   controlplane.KindFlags,
				Format: controlplane.FormatYAML,
				Body:   flagsBody(t, "rival2"),
			})
		}

		return nil
	}

	_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "mine")})
	require.ErrorIs(t, err, s3publish.ErrConflict)
}

// composite reports a composite checksum, as a multipart upload has, instead of the sha256 of the bytes.
type composite struct{ s3publish.API }

func (c composite) HeadObject(
	ctx context.Context, input *s3.HeadObjectInput, opts ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	out, err := c.API.HeadObject(ctx, input, opts...)
	if err == nil {
		out.ChecksumSHA256 = aws.String("Zm9v-3")
	}

	return out, err
}

func TestExistingObjectIsNeverAcceptedOnItsSizeAlone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr    error
		wrap       func(*s3fake.Fake) s3publish.API
		name       string
		wantObject string
		omit       bool
		corrupt    bool
	}{
		{name: "no checksum reported: the bytes are hashed and match", omit: true},
		{
			name:    "no checksum reported and same-size other bytes",
			omit:    true,
			corrupt: true,
			wantErr: s3publish.ErrObjectMismatch,
		},
		{
			name: "composite checksum: the bytes are hashed and match",
			wrap: func(f *s3fake.Fake) s3publish.API { return composite{f} },
		},
		{
			name:    "composite checksum and same-size other bytes",
			wrap:    func(f *s3fake.Fake) s3publish.API { return composite{f} },
			corrupt: true,
			wantErr: s3publish.ErrObjectMismatch,
		},
		{name: "a reported sha256 that differs", corrupt: true, wantErr: s3publish.ErrObjectMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			fake.OmitChecksums = tt.omit
			objects := standardObjects(t, "a")
			key := objectKey(t, objects[0].Body)

			stored := append([]byte{}, objects[0].Body...)
			if tt.corrupt {
				stored[0] ^= 0x01
			}

			fake.Seed(key, stored)

			var api s3publish.API = fake
			if tt.wrap != nil {
				api = tt.wrap(fake)
			}

			res, err := newPublisher(t, api, nil).Publish(t.Context(), s3publish.Request{Objects: objects[:1]})

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.NotContains(t, strings.Join(fake.Calls(), "\n"), "PUT "+manifestKey, "nothing is published")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, s3publish.StatusPresent, res.Objects[0].Status)
			assert.Contains(t, fake.Calls(), "GET "+key, "the object was read to hash it")
		})
	}
}

func TestNoChangeHealsAMissingObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantStatus s3publish.Status
		dryRun     bool
		wantPut    bool
	}{
		{name: "the missing object is uploaded again", wantStatus: s3publish.StatusUploaded, wantPut: true},
		{name: "a dry run only reports it", dryRun: true, wantStatus: s3publish.StatusWouldUpload},
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
			lost := objectKey(t, objects[1].Body)
			fake.Remove(lost)
			fake.ResetCalls()

			res, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects, DryRun: tt.dryRun})
			require.NoError(t, err)

			assert.False(t, res.Changed)
			assert.False(t, res.Written)
			assert.Equal(t, 1, res.Uploaded)
			assert.Equal(t, 1, res.Present)
			assert.Equal(t, int64(len(objects[1].Body)), res.UploadedBytes)
			assert.Equal(t, etag, fake.ETag(manifestKey), "the manifest is not rewritten")
			assert.Equal(t, uint64(1), res.Generation)

			statuses := map[string]s3publish.Status{}
			for _, o := range res.Objects {
				statuses[o.Name] = o.Status
			}

			assert.Equal(
				t,
				map[string]s3publish.Status{nameFlags: s3publish.StatusPresent, nameSets: tt.wantStatus},
				statuses,
			)

			_, found := fake.Body(lost)
			assert.Equal(t, tt.wantPut, found)
			assert.Equal(t, tt.wantPut, containsCall(fake.Calls(), "PUT "+lost+" create"))
			assert.False(t, containsCall(fake.Calls(), "PUT "+manifestKey))
			fake.AssertConsistent(t)
		})
	}
}

func containsCall(calls []string, prefix string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}

	return false
}

func TestNoChangeRefusesAnObjectWithOtherContent(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	pub := newPublisher(t, fake, nil)
	objects := standardObjects(t, "a")

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	key := objectKey(t, objects[0].Body)
	bad := append([]byte{}, objects[0].Body...)
	bad[0] ^= 0x01
	fake.Seed(key, bad)

	_, err = pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.ErrorIs(t, err, s3publish.ErrObjectMismatch)

	got, _ := fake.Body(key)
	assert.Equal(t, bad, got, "never overwritten")
}

func TestReplaceInvalidManifest(t *testing.T) {
	t.Parallel()

	oldSeed := func(t *testing.T, fake *s3fake.Fake) {
		t.Helper()
		fake.Seed(manifestKey, []byte("not a manifest"))
	}
	overLimit := func(t *testing.T, fake *s3fake.Fake) {
		t.Helper()
		seedBucket(
			t,
			fake,
			9,
			s3publish.Object{
				Name:   "a",
				Kind:   controlplane.KindFlags,
				Format: controlplane.FormatYAML,
				Body:   flagsBody(t, "x"),
			},
			s3publish.Object{
				Name:   "b",
				Kind:   controlplane.KindFlags,
				Format: controlplane.FormatYAML,
				Body:   flagsBody(t, "y"),
			},
		)
	}

	tests := []struct {
		wantErr     error
		seed        func(*testing.T, *s3fake.Fake)
		mutate      func(*s3publish.Config)
		name        string
		wantInErr   string
		request     s3publish.Request
		wantGen     uint64
		wantWrite   bool
		wantOnlyGet bool
	}{
		{
			name: "without the flag the error names it", seed: oldSeed, wantErr: s3publish.ErrPreviousManifest,
			wantInErr: "--replace-invalid-manifest", wantOnlyGet: true,
		},
		{
			name:        "the flag needs an explicit generation",
			seed:        oldSeed,
			request:     s3publish.Request{ReplaceInvalidManifest: true},
			wantErr:     s3publish.ErrGeneration,
			wantOnlyGet: true,
		},
		{
			name: "garbage is replaced", seed: oldSeed, wantGen: 5, wantWrite: true,
			request: s3publish.Request{ReplaceInvalidManifest: true, Generation: 5},
		},
		{
			name: "a manifest over the current limits is replaced", seed: overLimit, wantGen: 12, wantWrite: true,
			mutate:  func(c *s3publish.Config) { c.MaxObjects = 1 },
			request: s3publish.Request{ReplaceInvalidManifest: true, Generation: 12},
		},
		{
			name:        "a manifest over the current limits locks the channel without the flag",
			seed:        overLimit,
			mutate:      func(c *s3publish.Config) { c.MaxObjects = 1 },
			wantErr:     s3publish.ErrPreviousManifest,
			wantOnlyGet: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := s3fake.New(manifestKey)
			tt.seed(t, fake)

			etag := fake.ETag(manifestKey)
			fake.ResetCalls()

			req := tt.request
			req.Objects = standardObjects(t, "a")[:1]

			res, err := newPublisher(t, fake, tt.mutate).Publish(t.Context(), req)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Contains(t, err.Error(), tt.wantInErr)
				assert.Equal(t, []string{"GET " + manifestKey}, fake.Calls())
				assert.Equal(t, etag, fake.ETag(manifestKey))

				return
			}

			require.NoError(t, err)
			assert.True(t, res.Written)
			assert.Equal(t, tt.wantGen, res.Generation)
			assert.Contains(t, fake.Calls(), "PUT "+manifestKey+" if-match="+etag, "still conditional on the ETag read")

			stored, _ := fake.Body(manifestKey)
			manifest, _, err := controlplane.UnmarshalManifest(stored)
			require.NoError(t, err)
			assert.Equal(t, tt.wantGen, manifest.Generation)

			// The channel works again: the next publish needs no flag.
			next, err := newPublisher(
				t,
				fake,
				tt.mutate,
			).Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "b")[:1]})
			require.NoError(t, err)
			assert.Equal(t, tt.wantGen+1, next.Generation)
		})
	}
}

func TestReplaceInvalidManifestCannotOverwriteAWriterInBetween(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	fake.Seed(manifestKey, []byte("not a manifest"))
	fake.Before = func(call string) error {
		if strings.HasPrefix(call, "PUT "+manifestKey) {
			seedBucket(t, fake, 8, s3publish.Object{
				Name:   nameRival,
				Kind:   controlplane.KindFlags,
				Format: controlplane.FormatYAML,
				Body:   flagsBody(t, nameRival),
			})
		}

		return nil
	}

	_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{
		Objects: standardObjects(t, "a")[:1], ReplaceInvalidManifest: true, Generation: 5,
	})
	require.ErrorIs(t, err, s3publish.ErrConflict)
}

func TestReplaceFlagIsIgnoredForAValidManifest(t *testing.T) {
	t.Parallel()

	fake := s3fake.New(manifestKey)
	pub := newPublisher(t, fake, nil)

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a"), Generation: 5})
	require.NoError(t, err)

	_, err = pub.Publish(
		t.Context(),
		s3publish.Request{Objects: standardObjects(t, "b"), Generation: 3, ReplaceInvalidManifest: true},
	)
	require.ErrorIs(t, err, s3publish.ErrGeneration, "a valid manifest keeps the generation rule")
}

// objectsNamed returns n distinct flag files named obj-a, obj-b, ... (sorted order is the manifest order).
func objectsNamed(t *testing.T, count int) []s3publish.Object {
	t.Helper()

	out := make([]s3publish.Object, count)
	for i := range out {
		letter := string(rune('a' + i))
		out[i] = s3publish.Object{
			Name:   "obj-" + letter,
			Kind:   controlplane.KindFlags,
			Format: controlplane.FormatYAML,
			Body:   flagsBody(t, letter),
		}
	}

	return out
}

// hooked runs head before every HeadObject, beforePut before every PutObject and afterPut after one that succeeded.
type hooked struct {
	s3publish.API

	head      func(ctx context.Context, key string) error
	beforePut func(ctx context.Context, key string)
	afterPut  func(key string)
}

func (h *hooked) PutObject(
	ctx context.Context, input *s3.PutObjectInput, opts ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	if h.beforePut != nil {
		h.beforePut(ctx, aws.ToString(input.Key))
	}

	out, err := h.API.PutObject(ctx, input, opts...)
	if err == nil && h.afterPut != nil {
		h.afterPut(aws.ToString(input.Key))
	}

	return out, err
}

func (h *hooked) HeadObject(
	ctx context.Context, input *s3.HeadObjectInput, opts ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	if h.head != nil {
		err := h.head(ctx, aws.ToString(input.Key))
		if err != nil {
			return nil, err
		}
	}

	return h.API.HeadObject(ctx, input, opts...)
}

func TestObjectCallsAreBoundedByConcurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		concurrency int
		objects     int
		wantPeak    int32
	}{
		{name: "a configured bound", concurrency: 3, objects: 10, wantPeak: 3},
		{name: "the default is 8", concurrency: 0, objects: 20, wantPeak: 8},
		{name: "fewer objects than the bound", concurrency: 8, objects: 2, wantPeak: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var inFlight, peak atomic.Int32

				release := make(chan struct{})
				api := &hooked{API: s3fake.New(manifestKey), head: func(ctx context.Context, _ string) error {
					now := inFlight.Add(1)
					defer inFlight.Add(-1)

					for {
						seen := peak.Load()
						if now <= seen || peak.CompareAndSwap(seen, now) {
							break
						}
					}

					select {
					case <-release:
					case <-ctx.Done():
					}

					return nil
				}}

				pub := newPublisher(t, api, func(c *s3publish.Config) { c.Concurrency = tt.concurrency })
				done := make(chan error, 1)
				docs := objectsNamed(t, tt.objects)

				go func() {
					_, err := pub.Publish(t.Context(), s3publish.Request{Objects: docs})
					done <- err
				}()

				synctest.Wait() // every goroutine is blocked: the calls that could start have started

				assert.Equal(t, tt.wantPeak, inFlight.Load(), "exactly the bound is in flight, no more")
				close(release)
				require.NoError(t, <-done)
				assert.Equal(t, tt.wantPeak, peak.Load())
			})
		})
	}
}

func TestObjectResultsAreInManifestOrderAndTheManifestIsLast(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		objects := objectsNamed(t, 4)
		firstKey, lastKey := objectKey(t, objects[0].Body), objectKey(t, objects[3].Body)
		lastDone := make(chan struct{})
		fake := s3fake.New(manifestKey)

		// The first object is written only after the last one was: the completion order is not the manifest order.
		api := &hooked{
			API: fake,
			beforePut: func(ctx context.Context, key string) {
				if key == firstKey {
					select {
					case <-lastDone:
					case <-ctx.Done():
					}
				}
			},
			afterPut: func(key string) {
				if key == lastKey {
					close(lastDone)
				}
			},
		}

		res, err := newPublisher(t, api, func(c *s3publish.Config) { c.Concurrency = 4 }).
			Publish(t.Context(), s3publish.Request{Objects: objects})
		require.NoError(t, err)

		names := make([]string, 0, len(res.Objects))
		for _, o := range res.Objects {
			names = append(names, o.Name)
		}

		assert.Equal(t, []string{"obj-a", "obj-b", "obj-c", "obj-d"}, names)

		calls := fake.Calls()
		last := calls[len(calls)-1]
		assert.Equal(t, "PUT "+manifestKey+" create", last, "the manifest is written last")

		for _, call := range calls[:len(calls)-1] {
			assert.NotContains(t, call, "PUT "+manifestKey)
		}

		assert.Equal(t, 4, res.Uploaded)
		fake.AssertConsistent(t)
	})
}

func TestFirstErrorCancelsTheOtherObjectCalls(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		objects := objectsNamed(t, 4)
		keys := make([]string, len(objects))

		for i := range objects {
			keys[i] = objectKey(t, objects[i].Body)
		}

		fake := s3fake.New(manifestKey)
		started := make(chan struct{})

		var canceled atomic.Bool

		api := &hooked{API: fake, head: func(ctx context.Context, key string) error {
			switch key {
			case keys[0]:
				close(started)
				<-ctx.Done()
				canceled.Store(true)

				return ctx.Err()
			case keys[1]:
				<-started

				return s3fake.APIError("ServiceUnavailable")
			}

			return nil
		}}

		_, err := newPublisher(t, api, func(c *s3publish.Config) { c.Concurrency = 2 }).
			Publish(t.Context(), s3publish.Request{Objects: objects})

		var op *s3publish.OperationError

		require.ErrorAs(t, err, &op)
		assert.Equal(t, "HeadObject", op.Operation)
		assert.Equal(t, keys[1], op.Key, "the error is the failure, not the cancellation it caused")
		assert.True(t, canceled.Load(), "the call that was waiting was canceled")

		calls := fake.Calls()
		assert.NotContains(t, strings.Join(calls, "\n"), keys[2], "no new call starts after the failure")
		assert.NotContains(t, strings.Join(calls, "\n"), keys[3])
		assert.False(t, containsCall(calls, "PUT "+manifestKey), "the manifest is not written")
	})
}
