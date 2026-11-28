package flagruntime

import (
	"fmt"
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// SetSegments is what the rules of a flag set can use as segments: the catalog of the generation, with the members
// of the static segments in memory. It is built when the flag set is built and never changed; an unchanged flag set
// keeps the same one, and the member sets are shared with the segments objects that hold them.
type SetSegments struct {
	// Catalog defines the segments of the flag set. A static segment is answered from memory; a dynamic segment has
	// a definition and no provider yet, so it answers by its failure policy and the evaluation reports
	// NOT_CONFIGURED.
	Catalog *segment.Catalog
	// Static and Dynamic are the numbers of segments of each kind.
	Static, Dynamic int
}

// SegmentCounts is the number of segments of a flag set, in the status and in /info.
type SegmentCounts struct {
	// Static is the number of static segments (members in the document).
	Static int `json:"static"`
	// Dynamic is the number of dynamic segments (members in a store).
	Dynamic int `json:"dynamic"`
}

func (s *SetSegments) counts() *SegmentCounts {
	if s == nil {
		return nil
	}

	return &SegmentCounts{Static: s.Static, Dynamic: s.Dynamic}
}

// memberSets builds the member sets of the static segments of a document, once for the checksum of the object.
func memberSets(doc *controlplane.SegmentsDocument) map[string]*segment.MemberSet {
	var sets map[string]*segment.MemberSet

	for i := range doc.Segments {
		def := &doc.Segments[i]
		if def.Connection != "" {
			continue
		}

		if sets == nil {
			sets = make(map[string]*segment.MemberSet)
		}

		sets[def.Name] = segment.NewMemberSet(def.Members)
	}

	return sets
}

// setSegments resolves the segment sources of a flag set against the segments objects of the stage and builds its
// catalog. A source that is not a loaded segments object is a problem; the other sources still count. A name that
// two sources define is defined by the later one, as for flags.
func (st *stage) setSegments(spec *setSpec) ([]*segState, *SetSegments, []Problem) {
	if len(spec.segSources) == 0 {
		return nil, nil, nil
	}

	var (
		problems []Problem
		defs     = map[string]*controlplane.SegmentDefinition{}
		sets     = map[string]*segment.MemberSet{}
		inputs   = make([]*segState, len(spec.segSources))
	)

	for idx, source := range spec.segSources {
		held := st.segs[source]
		inputs[idx] = held

		if held == nil || !held.loaded {
			problems = append(problems, Problem{
				Code: controlplane.CodeUnresolvedSource, Subject: source,
				Reason: "the segment source is not a segments object that was loaded",
			})

			continue
		}

		for i := range held.doc.Segments {
			def := &held.doc.Segments[i]
			defs[def.Name] = def
			delete(sets, def.Name)

			if set, ok := held.sets[def.Name]; ok {
				sets[def.Name] = set
			}
		}
	}

	out := &SetSegments{}
	list := make([]segment.Definition, 0, len(defs))

	for _, name := range sortedNames(defs) {
		def := defs[name]
		if def.Connection == "" {
			out.Static++
		} else {
			out.Dynamic++
		}

		list = append(list, definitionOf(def))
	}

	catalog, err := segment.NewCatalogWithSets(sets, list...)
	if err != nil {
		problems = append(problems, Problem{
			Code: controlplane.CodeUnresolvedSource, Reason: fmt.Sprintf("the segments of the flag set: %v", err),
		})

		return inputs, nil, problems
	}

	out.Catalog = catalog

	return inputs, out, problems
}

func sortedNames(defs map[string]*controlplane.SegmentDefinition) []string {
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// definitionOf is the definition of the catalog for a document definition. A dynamic segment is a redis segment
// until the connection of the bootstrap file tells its kind; no provider serves it yet.
func definitionOf(def *controlplane.SegmentDefinition) segment.Definition {
	out := segment.Definition{
		Name: def.Name, Type: segment.TypeFile, OnError: segment.OnError(def.OnError),
	}

	if def.Connection != "" {
		out.Type = segment.TypeRedis
		out.Mode = segment.Mode(def.Mode)
		out.MissingKey = segment.MissingKey(def.MissingKey)
		out.Layout = segment.Layout(def.Layout)
	}

	return out
}
