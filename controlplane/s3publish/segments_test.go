package s3publish_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

const (
	connAudiences = "audiences"
	nameSegments  = "checkout-segments"
)

func segmentsBody(t *testing.T, doc *controlplane.SegmentsDocument) []byte {
	t.Helper()

	body, err := controlplane.MarshalSegments(doc)
	require.NoError(t, err)

	return body
}

func objectsWithSegments(t *testing.T, doc *controlplane.SegmentsDocument) []s3publish.Object {
	t.Helper()

	return []s3publish.Object{
		{Name: nameFlags, Kind: controlplane.KindFlags, Format: controlplane.FormatYAML, Body: flagsBody(t, "a")},
		{
			Name:   nameSegments,
			Kind:   controlplane.KindSegments,
			Format: controlplane.FormatYAML,
			Body:   segmentsBody(t, doc),
		},
		{
			Name: nameSets, Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML,
			Body: flagSetBody(t, controlplane.FlagSet{
				Name: "checkout", Sources: []string{nameFlags}, SegmentSources: []string{nameSegments},
				APIKeys: []string{"canary-api-key-0001"},
			}),
		},
	}
}

func validSegments() *controlplane.SegmentsDocument {
	return &controlplane.SegmentsDocument{Segments: []controlplane.SegmentDefinition{
		{Name: "beta", Members: []string{"alice", "bob"}},
		{
			Name:       "churned",
			Connection: connAudiences,
			Version:    &controlplane.SegmentVersion{Key: "churned:version", Interval: "5s"},
		},
	}}
}

func TestPublishASegmentsObjectLikeAFlagObject(t *testing.T) {
	t.Parallel()

	fake := newFake()
	objects := objectsWithSegments(t, validSegments())
	keys := make([]string, len(objects))

	for i := range objects {
		keys[i] = objectKey(t, objects[i].Body)
	}

	res, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	assert.Equal(t, []string{
		"GET " + manifestKey,
		"HEAD " + keys[0], "PUT " + keys[0] + " create",
		"HEAD " + keys[1], "PUT " + keys[1] + " create",
		"HEAD " + keys[2], "PUT " + keys[2] + " create",
		"PUT " + manifestKey + " create",
	}, fake.Calls(), "every object is uploaded before the manifest")
	assert.Equal(t, 3, res.Uploaded)

	stored, ok := fake.Body(manifestKey)
	require.True(t, ok)

	manifest, _, err := controlplane.UnmarshalManifest(stored)
	require.NoError(t, err)
	require.NoError(t, controlplane.ValidateManifest(manifest))
	require.NoError(t, controlplane.ValidateFlagSet(mustFlagSet(t, objects[2].Body), manifest))

	assert.Equal(t, controlplane.ManifestObject{
		Name:       nameSegments,
		Kind:       controlplane.KindSegments,
		Connection: testConnection,
		Key:        keys[1],
		Format:     controlplane.FormatYAML,
		Checksum:   controlplane.ChecksumOf(objects[1].Body),
		Size:       int64(len(objects[1].Body)),
	}, manifest.Objects[1], "the key is content addressed")

	body, found := fake.Body(keys[1])
	require.True(t, found)
	assert.Equal(t, objects[1].Body, body)

	fake.AssertConsistent(t)

	report, err := newPublisher(t, fake, nil).Verify(t.Context())
	require.NoError(t, err)
	assert.Empty(t, report.Problems, "the verification reads the segments object back and validates it")
}

func mustFlagSet(t *testing.T, body []byte) *controlplane.FlagSetDocument {
	t.Helper()

	doc, _, err := controlplane.UnmarshalFlagSet(body)
	require.NoError(t, err)

	return doc
}

func TestPublishRefusesAnInvalidSegmentsObject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		doc      *controlplane.SegmentsDocument
		name     string
		wantText string
	}{
		{
			name: "the keys layout with the snapshot mode",
			doc: &controlplane.SegmentsDocument{Segments: []controlplane.SegmentDefinition{
				{Name: "staff", Connection: connAudiences, Layout: "keys"},
			}},
			wantText: "the keys layout is lookup-only",
		},
		{
			name: "both a connection and members",
			doc: &controlplane.SegmentsDocument{Segments: []controlplane.SegmentDefinition{
				{Name: "both", Connection: connAudiences, Members: []string{"a"}},
			}},
			wantText: "has both a connection",
		},
		{
			name: "a duplicate name",
			doc: &controlplane.SegmentsDocument{
				Segments: []controlplane.SegmentDefinition{
					{Name: "a", Members: []string{}},
					{Name: "a", Members: []string{}},
				},
			},
			wantText: "duplicate name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake()
			objects := objectsWithSegments(t, tt.doc)

			_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
			require.ErrorIs(t, err, s3publish.ErrInvalid)
			require.ErrorContains(t, err, nameSegments)
			require.ErrorContains(t, err, tt.wantText)

			for _, call := range fake.Calls() {
				assert.NotContains(t, call, "PUT", "nothing is written for an invalid document")
			}
		})
	}
}

func TestPublishRefusesASegmentSourceThatTheManifestDoesNotList(t *testing.T) {
	t.Parallel()

	objects := objectsWithSegments(t, validSegments())
	objects = append(objects[:1], objects[2]) // the segments object is left out

	_, err := newPublisher(t, newFake(), nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.ErrorIs(t, err, s3publish.ErrInvalid)
	require.ErrorContains(t, err, "is not an object of kind segments")
}

var errTogether = errors.New("the documents do not fit together")

func TestCheckAllSeesEveryObjectAndRefusesThePublication(t *testing.T) {
	t.Parallel()

	var seen [][]string

	fake := newFake()
	objects := objectsWithSegments(t, validSegments())

	pub := newPublisher(t, fake, func(cfg *s3publish.Config) {
		cfg.CheckAll = func(all []s3publish.Object) error {
			names := make([]string, 0, len(all))
			for i := range all {
				names = append(names, all[i].Kind+":"+all[i].Name)
			}

			seen = append(seen, names)

			return errTogether
		}
	})

	_, err := pub.Publish(t.Context(), s3publish.Request{Objects: objects})
	require.ErrorIs(t, err, s3publish.ErrInvalid)
	require.ErrorIs(t, err, errTogether)
	assert.Equal(t, [][]string{{"flags:" + nameFlags, "segments:" + nameSegments, "flagSets:" + nameSets}}, seen,
		"one call with every object, in order")

	for _, call := range fake.Calls() {
		assert.NotContains(t, call, "PUT", "nothing is written")
	}
}

func TestVerifyRunsCheckAllOnTheObjectsItRead(t *testing.T) {
	t.Parallel()

	fake := newFake()
	objects := objectsWithSegments(t, validSegments())

	_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	tests := []struct {
		checkErr     error
		name         string
		wantObjects  []string
		wantProblems []s3publish.Problem
	}{
		{
			name: "the documents fit", wantObjects: []string{nameFlags, nameSegments, nameSets},
			wantProblems: []s3publish.Problem{},
		},
		{
			name: "they do not", checkErr: errTogether, wantObjects: []string{nameFlags, nameSegments, nameSets},
			wantProblems: []s3publish.Problem{{
				Object: "objects", Code: s3publish.ProblemInvalidContent, Detail: errTogether.Error(),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			pub := newPublisher(t, fake, func(cfg *s3publish.Config) {
				cfg.CheckAll = func(all []s3publish.Object) error {
					for i := range all {
						got = append(got, all[i].Name)
					}

					return tt.checkErr
				}
			})

			report, verifyErr := pub.Verify(t.Context())
			require.NoError(t, verifyErr)
			assert.Equal(t, tt.wantObjects, got)
			assert.Equal(t, tt.wantProblems, append([]s3publish.Problem{}, report.Problems...))
		})
	}
}

// seedSharedObject seeds a bucket whose manifest lists names that all point at one object.
func seedSharedObject(t *testing.T, names int, body []byte) *s3fake.Fake {
	t.Helper()

	fake := newFake()
	key := objectKey(t, body)
	fake.Seed(key, body)

	manifest := &controlplane.Manifest{Channel: testChannel, Generation: 1}

	for i := range names {
		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: fmt.Sprintf("flags-%03d", i), Kind: controlplane.KindFlags, Connection: testConnection, Key: key,
			Format: controlplane.FormatYAML, Checksum: controlplane.ChecksumOf(body), Size: int64(len(body)),
		})
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)
	fake.Seed(manifestKey, data)

	return fake
}

func TestVerifyKeepsOneBodyForObjectsOfOneChecksum(t *testing.T) {
	t.Parallel()

	const names = 512

	body := flagsBody(t, "shared")
	fake := seedSharedObject(t, names, body)

	var (
		count     int
		distinct  = map[*byte]struct{}{}
		retained  int
		nonShared int
	)

	pub := newPublisher(t, fake, func(cfg *s3publish.Config) {
		cfg.CheckAll = func(all []s3publish.Object) error {
			count = len(all)

			for i := range all {
				distinct[&all[i].Body[0]] = struct{}{}
				retained += len(all[i].Body)

				if len(all[i].Body) != len(body) {
					nonShared++
				}
			}

			return nil
		}
	})

	report, err := pub.Verify(t.Context())
	require.NoError(t, err)
	assert.Empty(t, report.Problems)
	assert.Equal(t, names, count, "every name is given to the hook")
	assert.Len(t, distinct, 1, "one body in memory for the 512 names")
	assert.Zero(t, nonShared)
	assert.Equal(t, names*len(body), retained, "the hook sees the content of each name, the memory holds it once")
}

func TestVerifyBoundsTheBodiesItKeepsForCheckAll(t *testing.T) {
	t.Parallel()

	first, second := flagsBody(t, "first"), flagsBody(t, "second")
	fake := newFake()

	manifest := &controlplane.Manifest{Channel: testChannel, Generation: 1}

	for i, body := range [][]byte{first, second} {
		key := objectKey(t, body)
		fake.Seed(key, body)

		manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
			Name: fmt.Sprintf("flags-%d", i), Kind: controlplane.KindFlags, Connection: testConnection, Key: key,
			Format: controlplane.FormatYAML, Checksum: controlplane.ChecksumOf(body), Size: int64(len(body)),
		})
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(t, err)
	fake.Seed(manifestKey, data)

	called := false

	pub := newPublisher(t, fake, func(cfg *s3publish.Config) {
		cfg.MaxTotalBytes = int64(len(first)) + 1 // room for one of the two bodies
		cfg.CheckAll = func([]s3publish.Object) error {
			called = true

			return nil
		}
	})

	report, err := pub.Verify(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, report.Problems)
	assert.Equal(
		t,
		"manifest",
		report.Problems[0].Object,
		"the sum of the distinct objects is over the limit of the reader",
	)
	assert.Contains(t, report.Problems[0].Detail, "over the limit")
	assert.False(t, called, "the hook does not run on a part of the objects")
	assert.Zero(t, report.Checked, "no object was read: no memory was spent on them")
}

type manyError struct{ list []error }

func (manyError) Error() string     { return "many" }
func (m manyError) Unwrap() []error { return m.list }

var errProblem = errors.New("problem")

func TestVerifyReportsOneProblemForEachErrorOfCheckAll(t *testing.T) {
	t.Parallel()

	fake := newFake()
	objects := objectsWithSegments(t, validSegments())

	_, err := newPublisher(t, fake, nil).Publish(t.Context(), s3publish.Request{Objects: objects})
	require.NoError(t, err)

	errs := make([]error, 30)
	for i := range errs {
		errs[i] = fmt.Errorf("%w number %d", errProblem, i)
	}

	pub := newPublisher(t, fake, func(cfg *s3publish.Config) {
		cfg.CheckAll = func([]s3publish.Object) error { return errors.Join(errs[:10]...) }
	})

	report, err := pub.Verify(t.Context())
	require.NoError(t, err)
	assert.Len(t, report.Problems, 10, "ten errors, ten problems, none cut")

	pub = newPublisher(t, fake, func(cfg *s3publish.Config) {
		cfg.CheckAll = func([]s3publish.Object) error { return manyError{list: errs} }
	})

	report, err = pub.Verify(t.Context())
	require.NoError(t, err)
	require.Len(t, report.Problems, s3publish.MaxCheckAllProblems+1)
	assert.Equal(t, "problem number 0", report.Problems[0].Detail)
	assert.Equal(t, "problem number 19", report.Problems[s3publish.MaxCheckAllProblems-1].Detail)
	assert.Equal(t, "and 10 more", report.Problems[s3publish.MaxCheckAllProblems].Detail)
}
