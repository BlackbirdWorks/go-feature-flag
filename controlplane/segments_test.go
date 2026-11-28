package controlplane_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	segLookup      = "lookup"
	segmentsHead   = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n"
	segConnection  = "audiences"
	segKeys        = "keys"
	segObjectA     = "segs-a"
	segBig         = "big"
	pathInterval   = "segments[0].version.interval"
	pathMaxEntries = "segments[0].cache.maxEntries"
	pathMembers    = "segments[0].members"
)

func dynamic(mutate func(*controlplane.SegmentDefinition)) controlplane.SegmentDefinition {
	seg := controlplane.SegmentDefinition{Name: "churned", Connection: segConnection}
	if mutate != nil {
		mutate(&seg)
	}

	return seg
}

func static(members ...string) controlplane.SegmentDefinition {
	return controlplane.SegmentDefinition{Name: "beta", Members: append([]string{}, members...)}
}

func TestValidateSegments(t *testing.T) {
	t.Parallel()

	tooMany := make([]string, controlplane.MaxSegmentMembers+1)
	for i := range tooMany {
		tooMany[i] = "m" + strings.Repeat(
			"x",
			2,
		) + string(
			rune('a'+i%26),
		) + strings.Repeat(
			"y",
			i/26%50,
		) + string(
			rune('0'+i%10),
		)
	}

	tests := []struct {
		name     string
		wantPath string
		wantText string
		segments []controlplane.SegmentDefinition
	}{
		{name: "a static segment", segments: []controlplane.SegmentDefinition{static("alice", "bob")}},
		{name: "a static segment with no member", segments: []controlplane.SegmentDefinition{static()}},
		{name: "a dynamic segment", segments: []controlplane.SegmentDefinition{dynamic(nil)}},
		{
			name: "a lookup segment with everything",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Mode, s.Layout, s.Key, s.MissingKey, s.OnError = segLookup, segKeys, "staff:", "unknown", "notMember"
				s.Version = &controlplane.SegmentVersion{Key: "staff:version", Interval: "1s"}
				s.Cache = &controlplane.SegmentCache{MaxEntries: 100, TTL: "30s"}
			})},
		},
		{
			name: "an invalid name", segments: []controlplane.SegmentDefinition{{Name: "Beta!", Members: []string{}}},
			wantPath: "segments[0].name", wantText: "is not 1 to 64 characters",
		},
		{
			name:     "a name that is too long",
			segments: []controlplane.SegmentDefinition{{Name: strings.Repeat("a", 65), Members: []string{}}},
			wantPath: "segments[0].name",
			wantText: "is not 1 to 64 characters",
		},
		{
			name: "both connection and members",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.Members = []string{"a"} }),
			},
			wantPath: "segments[0]",
			wantText: "has both a connection",
		},
		{
			name: "neither", segments: []controlplane.SegmentDefinition{{Name: "x"}},
			wantPath: "segments[0]", wantText: "needs a connection (dynamic segment) or members (static segment)",
		},
		{
			name: "a connection that is not a name",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.Connection = "Not A Name" }),
			},
			wantPath: "segments[0].connection",
			wantText: "does not match",
		},
		{
			name: "a bad mode",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.Mode = "stream" }),
			},
			wantPath: "segments[0].mode",
			wantText: "is not snapshot or lookup",
		},
		{
			name: "the keys layout with the default mode",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.Layout = segKeys }),
			},
			wantPath: "segments[0].layout",
			wantText: `segment "churned": layout "keys" with mode "snapshot": the keys layout is lookup-only`,
		},
		{
			name: "a bad layout",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.Layout = "tree" }),
			},
			wantPath: "segments[0].layout",
			wantText: "is not set or keys",
		},
		{
			name: "a bad missingKey",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.MissingKey = "empty" }),
			},
			wantPath: "segments[0].missingKey",
			wantText: "is not notMember or unknown",
		},
		{
			name: "a bad onError",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.OnError = "maybe" }),
			},
			wantPath: "segments[0].onError",
			wantText: "is not unknown, notMember or member",
		},
		{
			name: "an interval without a key",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Version = &controlplane.SegmentVersion{Value: "v1", Interval: "5s"}
			})},
			wantPath: pathInterval,
			wantText: "needs a key",
		},
		{
			name: "an interval that is not a duration",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Version = &controlplane.SegmentVersion{Key: "k", Interval: "soon"}
			})},
			wantPath: pathInterval,
			wantText: "is not a duration",
		},
		{
			name: "an interval that is too short",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Version = &controlplane.SegmentVersion{Key: "k", Interval: "1ms"}
			})},
			wantPath: pathInterval,
			wantText: "must be between",
		},
		{
			name: "an interval that is too long",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Version = &controlplane.SegmentVersion{Key: "k", Interval: "25h"}
			})},
			wantPath: pathInterval,
			wantText: "must be between",
		},
		{
			name: "a version key that is too long",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Version = &controlplane.SegmentVersion{Key: strings.Repeat("k", 513)}
			})},
			wantPath: "segments[0].version",
			wantText: "is longer than 512",
		},
		{
			name: "a store key that is too long",
			segments: []controlplane.SegmentDefinition{
				dynamic(func(s *controlplane.SegmentDefinition) { s.Key = strings.Repeat("k", 513) }),
			},
			wantPath: "segments[0].key",
			wantText: "is longer than 512",
		},
		{
			name: "a ttl that is not a duration",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Mode, s.Cache = segLookup, &controlplane.SegmentCache{TTL: "long"}
			})},
			wantPath: "segments[0].cache.ttl",
			wantText: "is not a duration",
		},
		{
			name: "a ttl above the bound",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Mode, s.Cache = segLookup, &controlplane.SegmentCache{TTL: "48h"}
			})},
			wantPath: "segments[0].cache.ttl",
			wantText: "must be above 0",
		},
		{
			name: "cache entries above the bound",
			segments: []controlplane.SegmentDefinition{dynamic(func(s *controlplane.SegmentDefinition) {
				s.Mode, s.Cache = segLookup, &controlplane.SegmentCache{MaxEntries: controlplane.MaxCacheEntries + 1}
			})},
			wantPath: pathMaxEntries,
			wantText: "must be between 0 and",
		},
		{
			name: "a dynamic field on a static segment", segments: []controlplane.SegmentDefinition{{
				Name: "beta", Members: []string{"a"}, Mode: segLookup,
			}},
			wantPath: "segments[0].mode", wantText: "is for a dynamic segment",
		},
		{
			name: "an empty member", segments: []controlplane.SegmentDefinition{static("a", "")},
			wantPath: "segments[0].members[1]", wantText: "is empty",
		},
		{
			name:     "a member that is too long",
			segments: []controlplane.SegmentDefinition{static(strings.Repeat("m", 4097))},
			wantPath: "segments[0].members[0]",
			wantText: "is longer than 4096",
		},
		{
			name: "too many members", segments: []controlplane.SegmentDefinition{{Name: segBig, Members: tooMany}},
			wantPath: pathMembers, wantText: "more than 100000",
		},
		{
			name: "members that are too many bytes", segments: []controlplane.SegmentDefinition{{
				Name: segBig, Members: bigMembers(),
			}},
			wantPath: pathMembers, wantText: "bytes, more than 4194304",
		},
		{
			name: "a duplicate name", segments: []controlplane.SegmentDefinition{static("a"), static("b")},
			wantPath: "segments[1].name", wantText: `duplicate name "beta", first used at segments[0].name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := controlplane.ValidateSegments(&controlplane.SegmentsDocument{Segments: tt.segments})
			if tt.wantPath == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, controlplane.ErrInvalid)

			var verr *controlplane.ValidationError

			require.ErrorAs(t, err, &verr)

			var got []string
			for _, problem := range verr.Problems {
				got = append(got, problem.Path+": "+problem.Reason)
			}

			assert.Contains(t, strings.Join(got, "\n"), tt.wantPath+": ")
			assert.Contains(t, strings.Join(got, "\n"), tt.wantText)
		})
	}
}

// bigMembers is a list of members that is within the count bound and over the byte bound.
func bigMembers() []string {
	members := make([]string, 0, 2000)

	for i := range 2000 {
		members = append(members, strings.Repeat("m", 2100)+string(rune('a'+i%26))+strings.Repeat("0", i/26+1))
	}

	return members
}

func TestValidateSegmentsHeaderAndNil(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, controlplane.ValidateSegments(nil), controlplane.ErrNilDocument)
	require.ErrorIs(
		t,
		controlplane.ValidateSegments(&controlplane.SegmentsDocument{Kind: frozenKindManifest}),
		controlplane.ErrWrongKind,
	)
	require.ErrorIs(
		t,
		controlplane.ValidateSegments(&controlplane.SegmentsDocument{APIVersion: "semaphore/v9"}),
		controlplane.ErrUnsupportedAPIVersion,
	)
}

func TestMarshalSegmentsKeepsDefaultsOut(t *testing.T) {
	t.Parallel()

	doc := &controlplane.SegmentsDocument{Segments: []controlplane.SegmentDefinition{
		static(),
		dynamic(func(s *controlplane.SegmentDefinition) { s.Key = "semaphore:segment:{churned}" }),
	}}

	want := `apiVersion: semaphore/v1
kind: Segments
segments:
  - name: beta
    members: []
  - name: churned
    connection: audiences
    key: semaphore:segment:{churned}
`

	got, err := controlplane.MarshalSegments(doc)
	require.NoError(t, err)
	assert.Equal(
		t,
		want,
		string(got),
		"no mode, layout or missingKey is written when it was not set, and an empty list is kept",
	)

	back, info, err := controlplane.UnmarshalSegments(got)
	require.NoError(t, err)
	assert.Empty(t, info.UnknownFields)
	assert.NotNil(t, back.Segments[0].Members, "an empty list stays a static segment with no member")
	assert.Nil(t, back.Segments[1].Members)
	assert.Empty(t, back.Segments[1].Mode)

	asJSON, err := controlplane.MarshalSegmentsJSON(doc)
	require.NoError(t, err)

	fromJSON, _, err := controlplane.UnmarshalSegments(asJSON)
	require.NoError(t, err)
	assert.Equal(t, back, fromJSON)

	_, err = controlplane.MarshalSegments(nil)
	require.ErrorIs(t, err, controlplane.ErrNilDocument)

	_, err = controlplane.MarshalSegmentsJSON(&controlplane.SegmentsDocument{Kind: frozenKindManifest})
	require.ErrorIs(t, err, controlplane.ErrWrongKind)
}

func TestUnmarshalSegmentsStrictErrorsNameThePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		text     string
		wantPath string
	}{
		{
			"members that are not a list",
			"apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: a\n    members: alice\n",
			pathMembers,
		},
		{
			"a cache entries that is not a number",
			segmentsHead + "  - name: a\n    connection: c\n    cache:\n      maxEntries: many\n",
			pathMaxEntries,
		},
		{
			"a fraction in an integer",
			segmentsHead + "  - name: a\n    connection: c\n    cache:\n      maxEntries: 1.5\n",
			pathMaxEntries,
		},
		{
			"a version that is not a mapping",
			"apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: a\n    connection: c\n    version: v1\n",
			"segments[0].version",
		},
		{"the wrong kind", "apiVersion: semaphore/v1\nkind: FlagSet\nsegments: []\n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, _, err := controlplane.UnmarshalSegments([]byte(tt.text))
			require.Error(t, err)

			if tt.wantPath == "" {
				require.ErrorIs(t, err, controlplane.ErrWrongKind)

				return
			}

			require.ErrorIs(t, err, controlplane.ErrDecode)
			assert.Contains(t, err.Error(), tt.wantPath)
		})
	}
}

func TestFlagSetSegmentSourcesResolveInTheManifest(t *testing.T) {
	t.Parallel()

	manifest := &controlplane.Manifest{
		Channel: "stable", Generation: 1,
		Objects: []controlplane.ManifestObject{
			object("flags-a", controlplane.KindFlags), object(segObjectA, controlplane.KindSegments),
			object("segs-b", controlplane.KindSegments),
		},
	}

	tests := []struct {
		name     string
		wantPath string
		wantText string
		sources  []string
	}{
		{name: "two segment objects", sources: []string{segObjectA, "segs-b"}},
		{name: "none", sources: nil},
		{
			name:     "an unknown name",
			sources:  []string{"nope"},
			wantPath: "flagSets[0].segmentSources[0]",
			wantText: "is not an object of kind segments",
		},
		{
			name:     "a flags object",
			sources:  []string{"flags-a"},
			wantPath: "flagSets[0].segmentSources[0]",
			wantText: "is not an object of kind segments",
		},
		{
			name:     "listed twice",
			sources:  []string{segObjectA, segObjectA},
			wantPath: "flagSets[0].segmentSources[1]",
			wantText: "is listed twice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := &controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{
				{Name: frozenSetCheckout, Sources: []string{"flags-a"}, SegmentSources: tt.sources},
			}}

			err := controlplane.ValidateFlagSet(doc, manifest)
			if tt.wantPath == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, controlplane.ErrInvalid)
			require.ErrorContains(t, err, tt.wantPath+": ")
			assert.ErrorContains(t, err, tt.wantText)
		})
	}

	// without a manifest nothing is resolved
	require.NoError(t, controlplane.ValidateFlagSet(&controlplane.FlagSetDocument{FlagSets: []controlplane.FlagSet{
		{Name: frozenSetCheckout, Sources: []string{"x"}, SegmentSources: []string{"anything"}},
	}}, nil))
}

func object(name, kind string) controlplane.ManifestObject {
	return controlplane.ManifestObject{
		Name:       name,
		Kind:       kind,
		Connection: "control",
		Key:        "objects/" + name + ".yaml",
		Format:     controlplane.FormatYAML,
		Checksum: controlplane.Checksum{
			Algorithm: controlplane.AlgorithmSHA256,
			Value:     strings.Repeat("a", 64),
		},
		Size: 10,
	}
}
