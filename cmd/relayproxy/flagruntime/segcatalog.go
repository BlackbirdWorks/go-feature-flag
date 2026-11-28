package flagruntime

import (
	"encoding/json"
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
	// hashes is the content hash of each segment of the catalog, members included: what a flag that uses the segment
	// depends on, see [FlagSet.flagSigs].
	hashes map[string]uint64
	// Static and Dynamic are the numbers of segments of each kind.
	Static, Dynamic int
	// RejectedDefinitions counts the dynamic definitions that the last content of its segments objects offered and the
	// relay rejected (the objects keep their last good content).
	RejectedDefinitions int
}

// SegmentCounts is the number of segments of a flag set, in the status and in /info.
type SegmentCounts struct {
	// Static is the number of static segments (members in the document).
	Static int `json:"static"`
	// Dynamic is the number of dynamic segments (members in a store) that are valid and served.
	Dynamic int `json:"dynamic"`
	// RejectedDefinitions is the number of dynamic definitions that were offered and rejected: the segments object
	// keeps its last good content.
	RejectedDefinitions int `json:"rejectedDefinitions,omitempty"`
}

func (s *SetSegments) counts() *SegmentCounts {
	if s == nil {
		return nil
	}

	return &SegmentCounts{Static: s.Static, Dynamic: s.Dynamic, RejectedDefinitions: s.RejectedDefinitions}
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
		hashes   = map[string]uint64{}
		inputs   = make([]*segState, len(spec.segSources))

		rejectedDefs int
	)

	for idx, source := range spec.segSources {
		held := st.segs[source]
		inputs[idx] = held

		if held != nil {
			rejectedDefs += held.rejectedDefs
		}

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
			hashes[def.Name] = held.hashes[def.Name]

			if set, ok := held.sets[def.Name]; ok {
				sets[def.Name] = set
			}
		}
	}

	out := &SetSegments{
		hashes: make(map[string]uint64, len(defs)), RejectedDefinitions: rejectedDefs,
	}
	list := make([]segment.Definition, 0, len(defs))

	for _, name := range sortedNames(defs) {
		def := defs[name]
		if def.Connection == "" {
			out.Static++
		} else {
			out.Dynamic++
		}

		list = append(list, definitionOf(def))
		out.hashes[name] = hashes[name]
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

// definitionHashes is the content hash of each definition of a document, the members of a static segment included,
// computed once for the checksum of the object. A flag that uses a segment depends on it.
func definitionHashes(doc *controlplane.SegmentsDocument) map[string]uint64 {
	hashes := make(map[string]uint64, len(doc.Segments))

	for i := range doc.Segments {
		data, err := json.Marshal(&doc.Segments[i])
		if err != nil {
			data = []byte(doc.Segments[i].Name) // cannot happen for this struct; the name still tells the segment
		}

		hashes[doc.Segments[i].Name] = hashBytes(data)
	}

	return hashes
}

// sigsHash is an order independent hash of the signatures of a flag set: a flag set whose segments changed has
// another hash, as one whose flags changed.
func sigsHash(sigs map[string]uint64) uint64 {
	var sum uint64

	for flag, sig := range sigs {
		sum += mixUint(hashString(flag), sig)
	}

	return sum
}

// flagSignatures is, for each flag that uses segments, a hash of the segments that it uses: it changes when the
// members or the definition of one of them change, or when a segment appears or goes away. Flags that use no segment
// have none, so the cost is proportional to the flags that use segments.
func flagSignatures(refs map[string][]string, segments *SetSegments) map[string]uint64 {
	if segments == nil || len(refs) == 0 {
		return nil
	}

	sigs := make(map[string]uint64, len(refs))

	for flag, names := range refs {
		sig := fnvOffset
		for _, name := range names {
			sig = mixUint(mixString(sig, name), segments.hashes[name])
		}

		sigs[flag] = sig
	}

	return sigs
}

// segUse is what the rules of one flag use of the segments: the names, sorted, and whether one is dynamic.
type segUse struct {
	names   []string
	dynamic bool
}

// flagUses is, for each flag that uses segments, the segments it uses and whether one of them is dynamic: a dynamic
// segment has its members in a store, so a client cannot hold the answer. Flags that use no segment have none.
func flagUses(refs map[string][]string, segments *SetSegments) map[string]segUse {
	if segments == nil || len(refs) == 0 {
		return nil
	}

	uses := make(map[string]segUse, len(refs))

	for flag, names := range refs {
		use := segUse{names: names}

		for _, name := range names {
			if def, ok := segments.Catalog.Lookup(name); ok && def.Type != segment.TypeFile {
				use.dynamic = true

				break
			}
		}

		uses[flag] = use
	}

	return uses
}
