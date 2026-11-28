package publish

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

const (
	objSegs         = "segs"
	flagsUsingGhost = "f:\n  variations: {\"on\": true, \"off\": false}\n  targeting:\n" +
		"    - name: r\n      query: targetingKey in segment(\"ghost\")\n      variation: \"on\"\n" +
		"  defaultRule: {variation: \"off\"}\n"
	segmentsWithGhost = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: ghost\n    members: []\n"
	setListing        = "apiVersion: semaphore/v1\nkind: FlagSet\nflagSets:\n  - name: s\n    sources: [flags]\n" +
		"    segmentSources: [segs]\n    apiKeys: [k]\n"
)

func TestCheckReferencesLooksOnlyAtWhatResolves(t *testing.T) {
	t.Parallel()

	flags := s3publish.Object{
		Name:   "flags",
		Kind:   controlplane.KindFlags,
		Format: controlplane.FormatYAML,
		Body:   []byte(flagsUsingGhost),
	}
	segs := s3publish.Object{
		Name:   objSegs,
		Kind:   controlplane.KindSegments,
		Format: controlplane.FormatYAML,
		Body:   []byte(segmentsWithGhost),
	}
	set := s3publish.Object{
		Name:   "sets",
		Kind:   controlplane.KindFlagSets,
		Format: controlplane.FormatYAML,
		Body:   []byte(setListing),
	}

	tests := []struct {
		name    string
		wantErr string
		objects []s3publish.Object
	}{
		{name: "everything is defined", objects: []s3publish.Object{flags, segs, set}},
		{
			name:    "the segment source is missing: left to the validation of the documents",
			objects: []s3publish.Object{flags, set},
			wantErr: "segment ghost: is not defined",
		},
		{
			name: "a segment source that does not decode is left to its own validation",
			objects: []s3publish.Object{
				flags,
				{Name: objSegs, Kind: controlplane.KindSegments, Body: []byte("segments: [")},
				set,
			},
			wantErr: "segment ghost: is not defined",
		},
		{
			name: "a flag-set document that does not decode is left to its own validation",
			objects: []s3publish.Object{
				flags,
				segs,
				{Name: "sets", Kind: controlplane.KindFlagSets, Body: []byte("flagSets: [")},
			},
		},
		{
			name: "a source of another kind is not a flag file",
			objects: []s3publish.Object{
				{Name: "flags", Kind: controlplane.KindSegments, Body: []byte(segmentsWithGhost)},
				{
					Name:   objSegs,
					Kind:   controlplane.KindFlags,
					Format: controlplane.FormatYAML,
					Body:   []byte(flagsUsingGhost),
				},
				set,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := checkReferences(tt.objects)
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tt.wantErr)
		})
	}

	require.NoError(
		t,
		checkSegments(&s3publish.Object{Kind: controlplane.KindSegments, Body: []byte(segmentsWithGhost)}),
	)
	require.Error(t, checkSegments(&s3publish.Object{Kind: controlplane.KindSegments, Body: []byte("segments: [")}))
}
