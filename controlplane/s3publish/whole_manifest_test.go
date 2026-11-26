package s3publish_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

// A rival that wrote the same channel, generation and entries but another createdAt wrote another manifest: it is a
// conflict. Only a manifest equal as a whole is ours.
func TestConflictIsOursOnlyWhenTheWholeManifestIsEqual(t *testing.T) {
	t.Parallel()

	noon := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	sameInstant := noon.In(time.FixedZone("plus2", 2*60*60))
	later := noon.Add(time.Second)

	tests := []struct {
		mine         *time.Time
		rival        *time.Time
		name         string
		wantConflict bool
	}{
		{name: "both without createdAt", mine: nil, rival: nil},
		{name: "same createdAt", mine: &noon, rival: &noon},
		{name: "same instant in another zone", mine: &noon, rival: &sameInstant},
		{name: "another createdAt", mine: &noon, rival: &later, wantConflict: true},
		{name: "rival has none", mine: &noon, rival: nil, wantConflict: true},
		{name: "rival has one, we have none", mine: nil, rival: &noon, wantConflict: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			objects := standardObjects(t, "a")

			rivalFake := newFake()
			_, err := newPublisher(t, rivalFake, nil).Publish(
				t.Context(), s3publish.Request{Objects: objects, Generation: 1, CreatedAt: tt.rival})
			require.NoError(t, err)

			rivalBody, ok := rivalFake.Body(manifestKey)
			require.True(t, ok)

			fake := newFake()
			fake.Before = func(call string) error {
				if strings.HasPrefix(call, "PUT "+manifestKey) {
					fake.Seed(manifestKey, rivalBody)
				}

				return nil
			}

			_, err = newPublisher(t, fake, nil).Publish(
				t.Context(), s3publish.Request{Objects: objects, Generation: 1, CreatedAt: tt.mine})

			if tt.wantConflict {
				require.ErrorIs(t, err, s3publish.ErrConflict)

				return
			}

			require.NoError(t, err)
		})
	}
}

// With the limit at MaxInt64 the reader must not wrap: the manifest and the objects are read in full.
func TestReadLimitDoesNotOverflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(*s3publish.Config)
		name   string
	}{
		{name: "defaults", mutate: func(*s3publish.Config) {}},
		{name: "manifest limit at MaxInt64", mutate: func(c *s3publish.Config) { c.MaxManifestBytes = math.MaxInt64 }},
		{name: "all limits at MaxInt64", mutate: func(c *s3publish.Config) {
			c.MaxManifestBytes = math.MaxInt64
			c.MaxObjectBytes = math.MaxInt64
			c.MaxTotalBytes = math.MaxInt64
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			pub := newPublisher(t, fake, tt.mutate)

			_, err := pub.Publish(t.Context(), s3publish.Request{Objects: standardObjects(t, "a")})
			require.NoError(t, err)

			rep, err := pub.Verify(t.Context())
			require.NoError(t, err)
			assert.True(t, rep.OK(), "%+v", rep.Problems)
			assert.Equal(t, 2, rep.Checked)
		})
	}
}

// The count and the size of each object are checked before any per-object work, so that a request over the limits is
// refused without hashing its bodies: the error is the limit, not what the first object would have said.
func TestRequestLimitsAreCheckedBeforeAnyObjectIsHandled(t *testing.T) {
	t.Parallel()

	badFormat := s3publish.Object{Name: "bad", Kind: controlplane.KindFlags, Format: "toml", Body: []byte("x")}
	good := s3publish.Object{
		Name:   "good",
		Kind:   controlplane.KindFlags,
		Format: controlplane.FormatYAML,
		Body:   []byte("flag: 1\n"),
	}

	tests := []struct {
		mutate    func(*s3publish.Config)
		name      string
		wantInErr string
		objects   []s3publish.Object
	}{
		{
			name:      "too many objects, the first is also bad",
			mutate:    func(c *s3publish.Config) { c.MaxObjects = 1 },
			objects:   []s3publish.Object{badFormat, good},
			wantInErr: "2 objects, the limit is 1",
		},
		{
			name:      "an oversize object after a bad one",
			mutate:    func(c *s3publish.Config) { c.MaxObjectBytes = 4 },
			objects:   []s3publish.Object{badFormat, good},
			wantInErr: "over the limit of 4 bytes",
		},
		{
			name: "an empty object after a bad one",
			objects: []s3publish.Object{
				badFormat,
				{Name: nameEmpty, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML},
			},
			wantInErr: "must not be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			_, err := newPublisher(t, fake, tt.mutate).Publish(t.Context(), s3publish.Request{Objects: tt.objects})

			require.ErrorIs(t, err, s3publish.ErrInvalid)
			require.ErrorIs(t, err, s3publish.ErrLimit)
			assert.Contains(t, err.Error(), tt.wantInErr)
			assert.Empty(t, fake.Calls(), "nothing was sent")
		})
	}
}

func TestVerifyProblemsAreInManifestOrderAndANoEntryProblemIsLast(t *testing.T) {
	t.Parallel()

	fake := newFake()
	sets := flagSetBody(t, controlplane.FlagSet{Name: "x", Sources: []string{nameNope}})
	flags := flagsBody(t, "a")

	// Names sort as a-sets, b-flags, c-flags: the cross-document problem belongs to the first entry and must come
	// before the problem of the second.
	manifest := &controlplane.Manifest{Channel: "other", Generation: 1}

	for _, obj := range []struct {
		name string
		kind string
		body []byte
	}{
		{"a-sets", controlplane.KindFlagSets, sets},
		{"b-flags", controlplane.KindFlags, flags},
		{"c-flags", controlplane.KindFlags, flags},
	} {
		key := objectKey(t, obj.body)
		fake.Seed(key, obj.body)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: obj.name, Kind: obj.kind, Connection: testConnection, Key: key, Format: controlplane.FormatYAML,
			Checksum: controlplane.ChecksumOf(obj.body), Size: int64(len(obj.body)),
		})
	}

	// b-flags is made unreadable by size: it reports a size mismatch.
	manifest.Objects[1].Size++

	body, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)
	fake.Seed(manifestKey, body)

	rep, err := newPublisher(t, fake, nil).Verify(t.Context())
	require.NoError(t, err)

	got := make([]string, 0, len(rep.Problems))
	for _, p := range rep.Problems {
		got = append(got, p.Object+" "+p.Code)
	}

	assert.Equal(t, []string{
		"a-sets " + string(controlplane.CodeUnresolvedSource),
		"b-flags " + s3publish.ProblemSizeMismatch,
		"manifest " + s3publish.ProblemManifestInvalid,
	}, got)
}

// A hook that rejects one document does not stop the checks across documents: the document is still decoded and
// takes part in them. The hook text is one line and bounded.
func TestHookRejectionKeepsTheDocumentForCrossChecksAndIsBounded(t *testing.T) {
	t.Parallel()

	sets := flagSetBody(t, controlplane.FlagSet{Name: "x", Sources: []string{nameNope}})
	flags := flagsBody(t, "a")
	long := strings.Repeat("y", 5*s3publish.MaxHookDetail)

	tests := []struct {
		name       string
		hookText   string
		wantDetail string
	}{
		{name: "short", hookText: "bad flag", wantDetail: "bad flag"},
		{
			name:       "newlines and controls",
			hookText:   "line one\nline two\t\x1b[31mred\r\n",
			wantDetail: "line one line two [31mred",
		},
		{name: "long", hookText: long, wantDetail: strings.Repeat("y", s3publish.MaxHookDetail) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			manifest := &controlplane.Manifest{Channel: testChannel, Generation: 1}

			for _, obj := range []struct {
				name string
				kind string
				body []byte
			}{
				{"a-flags", controlplane.KindFlags, flags},
				{"b-sets", controlplane.KindFlagSets, sets},
			} {
				key := objectKey(t, obj.body)
				fake.Seed(key, obj.body)

				manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
					Name:       obj.name,
					Kind:       obj.kind,
					Connection: testConnection,
					Key:        key,
					Format:     controlplane.FormatYAML,
					Checksum:   controlplane.ChecksumOf(obj.body),
					Size:       int64(len(obj.body)),
				})
			}

			body, err := controlplane.MarshalManifest(manifest)
			require.NoError(t, err)
			fake.Seed(manifestKey, body)

			pub := newPublisher(t, fake, func(c *s3publish.Config) {
				c.Check = func(obj *s3publish.Object) error {
					if obj.Kind == controlplane.KindFlagSets {
						return &hookError{text: tt.hookText}
					}

					return nil
				}
			})

			rep, err := pub.Verify(t.Context())
			require.NoError(t, err)

			require.Len(t, rep.Problems, 2)
			assert.Equal(t, "b-sets", rep.Problems[0].Object)
			assert.Equal(t, s3publish.ProblemInvalidContent, rep.Problems[0].Code)
			assert.Equal(t, tt.wantDetail, rep.Problems[0].Detail)
			assert.Equal(t, "b-sets", rep.Problems[1].Object)
			assert.Equal(t, string(controlplane.CodeUnresolvedSource), rep.Problems[1].Code,
				"the rejected document still takes part in the checks across documents")
		})
	}
}

type hookError struct{ text string }

func (e *hookError) Error() string { return e.text }
