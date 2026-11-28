package s3publish_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
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
