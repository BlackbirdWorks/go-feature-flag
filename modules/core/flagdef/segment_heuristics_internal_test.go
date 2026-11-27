package flagdef

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

func TestCompiledQuerySegmentsFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "a call", query: `key in segment("a")`, want: true},
		{name: "an invalid call", query: `key in segment(a)`, want: true},
		{name: "an attribute named segment", query: `segment eq "x"`},
		{name: "an attribute named segment in a list", query: `segment in ["a"]`},
		{name: "the text in a string", query: `name eq "segment(\"a\")"`},
		{name: "a nested attribute", query: `a.segment eq "x"`},
		{name: "no segment at all", query: `age gt 1`},
		{name: "json with the operation", query: `{"inSegment": [{"var": "k"}, "a"]}`, want: true},
		{name: "json with the text in a string", query: `{"==": [{"var": "k"}, "inSegment"]}`},
		{name: "json without it", query: `{"==": [{"var": "k"}, 1]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := NewService()
			assert.Equal(t, tt.want, svc.compiledQuery(tt.query).segments)
		})
	}
}

func TestHasSegmentTermIsNotASubstringTest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "json key", query: `{"inSegment": [{"var": "k"}, "a"]}`, want: true},
		{name: "json nested key", query: `{"and": [{"!": {"inSegment": [{"var": "k"}, "a"]}}]}`, want: true},
		{name: "json string literal", query: `{"==": [{"var": "k"}, "inSegment"]}`},
		{name: "json variable named like it", query: `{"==": [{"var": "inSegment"}, 1]}`},
		{name: "rules call", query: `k in segment("a")`, want: true},
		{name: "rules attribute", query: `segment eq "a"`},
		{name: "rules string", query: `k eq "segment(x)"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, hasSegmentTerm(tt.query))
		})
	}
}

func TestSegmentStateConstantsAgreeWithTheParser(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uint8(segment.Unknown), uint8(parser.SegmentUnknown))
	assert.Equal(t, uint8(segment.NotMember), uint8(parser.SegmentNotMember))
	assert.Equal(t, uint8(segment.Member), uint8(parser.SegmentMember))
}
