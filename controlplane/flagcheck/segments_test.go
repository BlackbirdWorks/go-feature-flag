package flagcheck_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
)

const (
	layoutKeys      = "keys"
	objFlags        = "flags"
	flagKey         = "checkout"
	reasonUndefined = "is not defined in the segment sources of the flag set"
	connAudiences   = "audiences"
	segModeLookup   = "lookup"
	segBeta         = "beta"
	segStaff        = "staff"
)

// TestSegmentValidationParity runs one table of segments through the validation of the controlplane package and
// through the validation of the engine's definition, so the two cannot drift apart: for the fields that both know,
// both accept or both reject.
func TestSegmentValidationParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		seg  controlplane.SegmentDefinition
		name string
		want bool
	}{
		{name: "static", seg: controlplane.SegmentDefinition{Name: segBeta, Members: []string{"a"}}, want: true},
		{
			name: "dynamic default",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences},
			want: true,
		},
		{
			name: "lookup keys",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Mode:       segModeLookup,
				Layout:     layoutKeys,
			},
			want: true,
		},
		{
			name: "snapshot keys",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Mode:       "snapshot",
				Layout:     layoutKeys,
			},
		},
		{
			name: "default mode keys",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences, Layout: layoutKeys},
		},
		{
			name: "bad mode",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences, Mode: "stream"},
		},
		{
			name: "bad layout",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences, Layout: "tree"},
		},
		{
			name: "bad missingKey",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences, MissingKey: "empty"},
		},
		{
			name: "missingKey unknown",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences, MissingKey: "unknown"},
			want: true,
		},
		{
			name: "bad onError",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences, OnError: "maybe"},
		},
		{
			name: "onError member",
			seg:  controlplane.SegmentDefinition{Name: segBeta, Connection: connAudiences, OnError: "member"},
			want: true,
		},
		{name: "upper case name", seg: controlplane.SegmentDefinition{Name: "Beta", Members: []string{}}},
		{name: "empty name", seg: controlplane.SegmentDefinition{Members: []string{}}},
		{
			name: "name with underscore and dash",
			seg:  controlplane.SegmentDefinition{Name: "a_b-c", Members: []string{}},
			want: true,
		},
		{
			name: "version value",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Value: "v1"},
			},
			want: true,
		},
		{
			name: "version key interval",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Key: "k", Interval: "5s"},
			},
			want: true,
		},
		{
			name: "version both",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Value: "v", Key: "k"},
			},
		},
		{
			name: "interval without key",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Interval: "5s"},
			},
		},
		{
			name: "interval too short",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Key: "k", Interval: "1ms"},
			},
		},
		{
			name: "interval too long",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Key: "k", Interval: "48h"},
			},
		},
		{
			name: "debounce on a snapshot",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Key: "k", Debounce: "5s"},
			},
			want: true,
		},
		{
			name: "debounce off",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Mode:       segModeLookup,
				Version:    &controlplane.SegmentVersion{Key: "k", Debounce: "0s"},
			},
			want: true,
		},
		{
			name: "debounce on a lookup",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Mode:       segModeLookup,
				Version:    &controlplane.SegmentVersion{Key: "k", Debounce: "5s"},
			},
		},
		{
			name: "debounce without key",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Value: "v", Debounce: "5s"},
			},
		},
		{
			name: "negative debounce",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Key: "k", Debounce: "-1s"},
			},
		},
		{
			name: "interval at the lower bound",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Key: "k", Interval: "100ms"},
			},
			want: true,
		},
		{
			name: "interval at the upper bound",
			seg: controlplane.SegmentDefinition{
				Name:       segBeta,
				Connection: connAudiences,
				Version:    &controlplane.SegmentVersion{Key: "k", Interval: "24h"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			docErr := controlplane.ValidateSegments(
				&controlplane.SegmentsDocument{Segments: []controlplane.SegmentDefinition{tt.seg}},
			)

			def, ok := flagcheck.Definition(&tt.seg)
			engineErr := def.Validate()

			assert.Equal(t, tt.want, docErr == nil, "the controlplane validation: %v", docErr)
			assert.Equal(t, tt.want, ok && engineErr == nil, "the engine validation: %v", engineErr)
		})
	}
}

func TestCheckSegments(t *testing.T) {
	t.Parallel()

	good := "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n" +
		"  - name: beta\n    members: [a]\n  - name: staff\n    connection: audiences\n"
	require.NoError(t, flagcheck.CheckSegments([]byte(good)))

	for name, body := range map[string]string{
		"not a document":     "segments: [",
		"a document problem": "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n",
	} {
		require.ErrorIs(t, flagcheck.CheckSegments([]byte(body)), flagcheck.ErrSegments, name)
	}
}

const flagsWithSegments = `
checkout:
  variations:
    "on": true
    "off": false
  targeting:
    - name: beta rule
      query: targetingKey in segment("beta")
      variation: "on"
    - name: staff rule
      query: targetingKey in segment("staff") or targetingKey in segment("ghost")
      variation: "on"
  defaultRule:
    variation: "off"
`

func TestCheckSegmentReferences(t *testing.T) {
	t.Parallel()

	segments := func(names ...string) []flagcheck.SegmentFile {
		doc := &controlplane.SegmentsDocument{}
		for _, name := range names {
			doc.Segments = append(doc.Segments, controlplane.SegmentDefinition{Name: name, Members: []string{}})
		}

		return []flagcheck.SegmentFile{{Name: "segs", Doc: doc}}
	}

	flags := []flagcheck.FlagFile{{Name: objFlags, Format: "yaml", Body: []byte(flagsWithSegments)}}

	tests := []struct {
		name     string
		wantText string
		sources  []flagcheck.SegmentFile
		want     []flagcheck.ReferenceProblem
	}{
		{name: "everything is defined", sources: segments(segBeta, segStaff, "ghost")},
		{
			name: "one segment is not defined", sources: segments(segBeta, segStaff),
			want: []flagcheck.ReferenceProblem{{
				Object: objFlags, Flag: flagKey, Rule: 1, Segment: "ghost",
				Reason: reasonUndefined,
			}},
			wantText: "flag set checkout-set: flag checkout, rule 1: segment ghost: is not defined",
		},
		{
			name: "no segment source at all", sources: nil,
			want: []flagcheck.ReferenceProblem{
				{
					Object:  objFlags,
					Flag:    flagKey,
					Rule:    0,
					Segment: segBeta,
					Reason:  reasonUndefined,
				},
				{
					Object:  objFlags,
					Flag:    flagKey,
					Rule:    1,
					Segment: "ghost",
					Reason:  reasonUndefined,
				},
				{
					Object:  objFlags,
					Flag:    flagKey,
					Rule:    1,
					Segment: segStaff,
					Reason:  reasonUndefined,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := flagcheck.CheckSegmentReferences("checkout-set", flags, tt.sources)
			if tt.want == nil {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, flagcheck.ErrSegments)

			var refErr *flagcheck.ReferenceError

			require.ErrorAs(t, err, &refErr)
			assert.Equal(t, tt.want, refErr.Problems)

			if tt.wantText != "" {
				assert.ErrorContains(t, err, tt.wantText)
			}
		})
	}
}

func TestCheckSegmentReferencesRejectsANameDefinedTwice(t *testing.T) {
	t.Parallel()

	one := &controlplane.SegmentsDocument{
		Segments: []controlplane.SegmentDefinition{{Name: segBeta, Members: []string{}}},
	}
	two := &controlplane.SegmentsDocument{
		Segments: []controlplane.SegmentDefinition{{Name: segBeta, Members: []string{"a"}}},
	}

	err := flagcheck.CheckSegmentReferences("checkout-set", nil, []flagcheck.SegmentFile{
		{Name: "first", Doc: one}, {Name: "second", Doc: two},
	})

	var refErr *flagcheck.ReferenceError

	require.ErrorAs(t, err, &refErr)
	assert.Equal(t, []flagcheck.ReferenceProblem{{
		Object: "second", Segment: segBeta, Rule: -1,
		Reason: "defined twice in the segment sources of the flag set, first in first",
	}}, refErr.Problems)
	assert.ErrorContains(t, err, "flag set checkout-set: segment beta: defined twice")
}
