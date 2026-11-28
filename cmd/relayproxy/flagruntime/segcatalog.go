package flagruntime

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
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
	// View binds the served dynamic definitions of the catalog to the providers of the process, nil when none is
	// served. It is built with the catalog and carried over with it.
	View *SegmentView
	// hashes is the content hash of each segment of the catalog, members included: what a flag that uses the segment
	// depends on, see [FlagSet.flagSigs].
	hashes map[string]uint64
	// Static and Dynamic are the numbers of segments of each kind.
	Static, Dynamic int
	// RejectedDefinitions counts the dynamic definitions that the connections of the bootstrap file cannot serve:
	// they are rejected alone, stay in the catalog and answer as a segment without a provider.
	RejectedDefinitions int
}

// SegmentCounts is the number of segments of a flag set, in the status and in /info.
type SegmentCounts struct {
	// UnresolvedNames is the first of the unresolved names, sorted, at most [MaxUnresolvedNames]. Names only.
	UnresolvedNames []string `json:"unresolvedNames,omitempty"`
	// Static is the number of static segments (members in the document).
	Static int `json:"static"`
	// Dynamic is the number of dynamic segments (members in a store), the rejected ones included.
	Dynamic int `json:"dynamic"`
	// RejectedDefinitions is the number of dynamic definitions, among Dynamic, that the connections of the bootstrap
	// file cannot serve.
	RejectedDefinitions int `json:"rejectedDefinitions,omitempty"`
	// Unresolved is the number of segment names that the flags of the flag set use and that no segments object of
	// the flag set defines (a typo, or an object that is not in segmentSources): they answer "not a member" for ever.
	Unresolved int `json:"unresolved,omitempty"`
}

// MaxUnresolvedNames bounds the unresolved names that /info and the log list.
const MaxUnresolvedNames = 5

// unresolvedSegments returns the sorted, distinct segment names that the flags use and the flag set cannot resolve.
func unresolvedSegments(uses map[string]segUse) []string {
	seen := map[string]struct{}{}

	for _, use := range uses {
		for _, name := range use.missing {
			seen[name] = struct{}{}
		}
	}

	return slices.Sorted(maps.Keys(seen))
}

// withUnresolved adds the unresolved names of a flag set to the counts of its segments (which are nil for a flag set
// with no segments object).
func withUnresolved(counts *SegmentCounts, uses map[string]segUse) *SegmentCounts {
	names := unresolvedSegments(uses)
	if len(names) == 0 {
		return counts
	}

	if counts == nil {
		counts = &SegmentCounts{}
	}

	counts.Unresolved = len(names)
	counts.UnresolvedNames = names[:min(len(names), MaxUnresolvedNames)]

	return counts
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
		owner    = map[string]*segState{}
		hashes   = map[string]uint64{}
		inputs   = make([]*segState, len(spec.segSources))

		rejected = map[string]struct{}{}
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
			delete(rejected, def.Name)
			owner[def.Name] = held

			if _, bad := held.rejected[def.Name]; bad {
				rejected[def.Name] = struct{}{}
			}

			hashes[def.Name] = held.hashes[def.Name]

			if set, ok := held.sets[def.Name]; ok {
				sets[def.Name] = set
			}
		}
	}

	out := &SetSegments{hashes: make(map[string]uint64, len(defs)), RejectedDefinitions: len(rejected)}
	list := make([]segment.Definition, 0, len(defs))

	for _, name := range sortedNames(defs) {
		def := defs[name]
		if def.Connection == "" {
			out.Static++
		} else {
			out.Dynamic++
		}

		_, bad := rejected[name]
		list = append(list, definitionOf(def, bad))
		out.hashes[name] = hashes[name]
	}

	view, live, bindProblems := st.bindView(out, defs, rejected, owner)
	problems = append(problems, bindProblems...)

	catalog, err := segment.NewCatalogWithLive(sets, live, list...)
	if err != nil {
		problems = append(problems, Problem{
			Code: controlplane.CodeUnresolvedSource, Reason: fmt.Sprintf("the segments of the flag set: %v", err),
		})

		return inputs, nil, problems
	}

	out.Catalog, out.View = catalog, view

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
// until the connection of the bootstrap file tells its kind; no provider serves it yet. A rejected definition (its
// connection is missing or cannot serve it) always answers not a member, whatever its failure policy says: it is a
// configuration error of this relay, not an outage that the publisher chose a policy for, and it never grants
// membership (a rule that says onError: match does not match either, because the answer is not unknown).
func definitionOf(def *controlplane.SegmentDefinition, rejected bool) segment.Definition {
	out := segment.Definition{
		Name: def.Name, Type: segment.TypeFile, OnError: segment.OnError(def.OnError),
	}

	if def.Connection != "" {
		out.Type = segment.TypeRedis
		out.Mode = segment.Mode(def.Mode)
		out.MissingKey = segment.MissingKey(def.MissingKey)
		out.Layout = segment.Layout(def.Layout)
		out.Version = versionSpec(def.Version)
	}

	// Applied last: the missingKey of the document must not overwrite the rejection.
	if rejected {
		out.OnError = segment.OnErrorNotMember
		out.MissingKey = segment.MissingNotMember
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

// segUse is what the rules of one flag use of the segments: the names, sorted, whether one is dynamic (its members are
// in a store) and whether one cannot be resolved by the flag set (no source defines it).
type segUse struct {
	names      []string
	missing    []string
	dynamic    bool
	unresolved bool
}

// flagUses is, for each flag that uses segments, the segments it uses. A client cannot hold the answer of a flag that
// uses a dynamic segment (its members live in a store), nor of one that uses a name that the flag set cannot resolve.
// Flags that use no segment have none.
func flagUses(refs map[string][]string, segments *SetSegments) map[string]segUse {
	if len(refs) == 0 {
		return nil
	}

	uses := make(map[string]segUse, len(refs))

	for flag, names := range refs {
		use := segUse{names: names}

		for _, name := range names {
			if segments == nil || segments.Catalog == nil {
				use.unresolved = true
				use.missing = append(use.missing, name)

				continue
			}

			def, ok := segments.Catalog.Lookup(name)

			switch {
			case !ok:
				use.unresolved = true
				use.missing = append(use.missing, name)
			case def.Type != segment.TypeFile:
				use.dynamic = true
			}
		}

		uses[flag] = use
	}

	return uses
}

// placeholderCounts is what /info says of the segments of a flag set that is only a placeholder: the segments objects
// that its sources name, if any is loaded. It is nil when the flag set names none.
func (st *stage) placeholderCounts(spec *setSpec) *SegmentCounts {
	if len(spec.segSources) == 0 {
		return nil
	}

	counts := &SegmentCounts{}

	for _, source := range spec.segSources {
		held := st.segs[source]
		if held == nil || !held.loaded {
			continue
		}

		for i := range held.doc.Segments {
			if held.doc.Segments[i].Connection == "" {
				counts.Static++
			} else {
				counts.Dynamic++
			}
		}

		counts.RejectedDefinitions += len(held.rejected)
	}

	return counts
}

// nameSet is the set of names, nil for none.
func nameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}

	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}

	return set
}

// versionSpec is the version of a definition for the catalog; an interval that does not parse is left out (the
// document was validated before).
func versionSpec(version *controlplane.SegmentVersion) *segment.VersionSpec {
	if version == nil {
		return nil
	}

	spec := &segment.VersionSpec{Value: version.Value, Key: version.Key}

	if version.Interval != "" {
		interval, err := time.ParseDuration(version.Interval)
		if err == nil {
			spec.Interval = interval
		}
	}

	return spec
}

// bindView takes, for each dynamic definition that the relay can serve (a connection it knows), the binding of the
// segments object that defines it: built once for the object and shared by every flag set and generation, so a flag
// set binds nothing of its own. A definition that cannot be bound stays unserved and the flag set is degraded.
func (st *stage) bindView(
	out *SetSegments,
	defs map[string]*controlplane.SegmentDefinition,
	rejected map[string]struct{},
	owner map[string]*segState,
) (*SegmentView, map[string]segment.Live, []Problem) {
	if !st.b.opts.Segments.enabled() || out.Dynamic == out.RejectedDefinitions {
		return nil, nil, nil
	}

	var problems []Problem

	bindings := map[string]*segBinding{}

	for _, name := range sortedNames(defs) {
		def := defs[name]
		if _, bad := rejected[name]; bad || def.Connection == "" {
			continue
		}

		b, err := owner[name].binds.binding(name)
		if err != nil {
			problems = append(problems, Problem{
				Code: controlplane.CodeUnresolvedSource, Subject: name,
				Reason: fmt.Sprintf("the dynamic segment %q is not served: %v", name, err),
			})

			continue
		}

		if refusal, refused := b.refused[name]; refused {
			problems = append(problems, Problem{
				Code: controlplane.CodeUnresolvedSource, Subject: name,
				Reason: fmt.Sprintf("the dynamic segment %q is not served: %v", name, refusal),
			})

			continue
		}

		bindings[name] = b
	}

	view, live := newSegmentView(bindings)
	if view != nil {
		view.traced = st.b.opts.Segments.Traced
	}

	return view, live, problems
}

// dynamicTerms is, for each flag that uses a served dynamic segment, the segment terms of its rules that the view
// serves. It parses the rules of those flags only, when the flag set is built.
func dynamicTerms(
	svc *flagdef.Service, flags map[string]*flagdef.InternalFlag, uses map[string]segUse, segments *SetSegments,
) map[string][]segment.Term {
	if segments == nil || segments.View == nil {
		return nil
	}

	var out map[string][]segment.Term

	for key, use := range uses {
		if !use.dynamic {
			continue
		}

		flag, ok := flags[key]
		if !ok {
			continue
		}

		var terms []segment.Term

		for _, term := range flag.SegmentTerms(svc) {
			if segments.View.Serves(term.Name) {
				terms = append(terms, term)
			}
		}

		if len(terms) == 0 {
			continue
		}

		if out == nil {
			out = map[string][]segment.Term{}
		}

		out[key] = terms
	}

	return out
}
