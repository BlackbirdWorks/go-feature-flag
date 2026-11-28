package flagruntime

import (
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// objState is the state of one flags object. It is immutable once built; a changed object gets a new one.
type objState struct {
	flags     map[string]*flagdef.InternalFlag
	status    *ObjectStatus
	name      string
	format    string
	checksum  string // checksum of the content held in flags
	attempted string // cache key (format and checksum) of the last bytes that could not be decoded
	reason    string
	refKey    string // key of the parse cache entry this object holds
	state     ObjectState
	hashes    map[string]uint64   // content hash of every flag of flags
	refs      map[string][]string // the segments that the flags use, for the flags that use some
	rejected  []FlagRejection
	content   uint64 // order independent hash of the effective flags
	loaded    bool
}

// docState is the state of one flag-set document object.
type docState struct {
	parsed    *parsedDoc
	status    *ObjectStatus
	probs     map[int][]EntryProblem // problems per entry index, valid for sig
	name      string
	checksum  string
	attempted string
	reason    string
	refKey    string
	state     ObjectState
	sig       uint64
	validated bool
}

// setSpec is the definition of a flag set in force.
type setSpec struct {
	name    string
	sources []string
	// segSources are the names of the segments objects whose segments the rules of the flag set can use.
	segSources []string
	declKeys   []string // keys as declared
	keys       []string // keys the flag set holds (declared minus the ones another flag set holds)
	declHash   uint64
}

// setState is a built flag set with what it was built from.
type setState struct {
	spec   *setSpec
	fs     *FlagSet
	status *FlagSetStatus
	inputs []*objState
	// segInputs are the segments objects that the set was built from, by pointer, in the order of segSources.
	segInputs []*segState
	notes     []Problem
	// frozen: the entry is invalid, the set keeps the flags it had. placeholder: the entry is invalid and the
	// set never had flags; it only routes its keys.
	frozen      bool
	placeholder bool
}

// state is the committed state of the builder after a build.
type state struct {
	objStatus map[string]*ObjectStatus
	docs      map[string]*docState
	segs      map[string]*segState
	sets      map[string]*setState
	flagSets  map[string]*FlagSet
	byKey     map[string]*FlagSet
	rev       map[string][]string
	objects   map[string]*objState
	setStatus map[string]*FlagSetStatus
	public    *publicView
	entries   []EntryProblem
	withdrawn []WithdrawnSet
	namesVer  uint64
	init      bool
}

func emptyState() *state {
	return &state{
		objects:   map[string]*objState{},
		docs:      map[string]*docState{},
		segs:      map[string]*segState{},
		sets:      map[string]*setState{},
		flagSets:  map[string]*FlagSet{},
		byKey:     map[string]*FlagSet{},
		rev:       map[string][]string{},
		objStatus: map[string]*ObjectStatus{},
		setStatus: map[string]*FlagSetStatus{},
	}
}

func entryProblemOf(object string, fe *controlplane.FieldError) EntryProblem {
	return EntryProblem{Object: object, Name: fe.Name, Reason: fe.Reason, Code: fe.Code, Index: fe.Index}
}
