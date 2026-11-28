package publish

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

const (
	objSegs         = "segs"
	objSets         = "sets"
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
		Name:   objSets,
		Kind:   controlplane.KindFlagSets,
		Format: controlplane.FormatYAML,
		Body:   []byte(setListing),
	}

	otherSegs := s3publish.Object{
		Name: objSegs, Kind: controlplane.KindSegments, Format: controlplane.FormatYAML,
		Body: []byte("apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: other\n    members: []\n"),
	}
	// "shadow" defines the flag f without a segment rule
	shadow := s3publish.Object{
		Name: "shadow", Kind: controlplane.KindFlags, Format: controlplane.FormatYAML,
		Body: []byte("f:\n  variations: {\"on\": true, \"off\": false}\n  defaultRule: {variation: \"off\"}\n"),
	}
	setTwo := s3publish.Object{
		Name: objSets, Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML,
		Body: []byte(strings.Replace(setListing, "sources: [flags]", "sources: [flags, shadow]", 1)),
	}
	setTwoReversed := s3publish.Object{
		Name: objSets, Kind: controlplane.KindFlagSets, Format: controlplane.FormatYAML,
		Body: []byte(strings.Replace(setListing, "sources: [flags]", "sources: [shadow, flags]", 1)),
	}

	tests := []struct {
		name    string
		wantErr string
		objects []s3publish.Object
	}{
		{name: "everything is defined", objects: []s3publish.Object{flags, segs, set}},
		{
			name:    "the segment source is not read (verify skips another connection): its names are unknown",
			objects: []s3publish.Object{flags, set},
		},
		{
			name: "a segment source that does not decode is left to its own validation: its names are unknown",
			objects: []s3publish.Object{
				flags,
				{Name: objSegs, Kind: controlplane.KindSegments, Body: []byte("segments: [")},
				set,
			},
		},
		{
			name:    "a segment that is defined nowhere, all sources read (publish)",
			objects: []s3publish.Object{flags, otherSegs, set},
			wantErr: "segment ghost: is not defined",
		},
		{
			name:    "the shadowed flag uses the missing segment, the winner does not: nothing to report",
			objects: []s3publish.Object{flags, shadow, otherSegs, setTwo},
		},
		{
			name:    "the winner uses the missing segment: reported",
			objects: []s3publish.Object{shadow, flags, otherSegs, setTwoReversed},
			wantErr: "flag f, rule 0: segment ghost: is not defined",
		},
		{
			name: "a flag-set document that does not decode is left to its own validation",
			objects: []s3publish.Object{
				flags,
				segs,
				{Name: objSets, Kind: controlplane.KindFlagSets, Body: []byte("flagSets: [")},
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
