package segment

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
)

// State is what a Provider knows of one lookup. The zero value is Unknown. A provider must only set one of the
// three constants; the engine treats any other value as Unknown.
type State uint8

const (
	// Unknown means the provider could not tell: the store is down, the lookup timed out, the segment is not loaded.
	Unknown State = iota
	// NotMember means the value is not in the segment.
	NotMember
	// Member means the value is in the segment.
	Member
)

// String implements [fmt.Stringer].
func (s State) String() string {
	switch s {
	case Unknown:
		return "unknown"
	case NotMember:
		return "notMember"
	case Member:
		return "member"
	}

	return "State(" + strconv.Itoa(int(s)) + ")"
}

// Valid reports whether the state is one of the three constants.
func (s State) Valid() bool { return s <= Member }

const (
	// MaxNameLen is the longest name of a segment.
	MaxNameLen = 64
)

// ValidName reports whether name is a segment name: 1 to 64 characters of a-z, 0-9, "_" and "-".
func ValidName(name string) bool {
	if name == "" || len(name) > MaxNameLen {
		return false
	}

	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}

	return true
}

// Term is a segment term of a flag: the attribute whose value is looked up, in the segment Name.
type Term struct {
	// Name is the segment.
	Name string
	// Path is the attribute, one element for each level of a nested attribute ("user.email" is {"user", "email"}).
	Path []string
}

// Lookup is one membership question: is ID a member of Segment. The provider writes the answer in State.
type Lookup struct {
	// Segment is the name of the segment.
	Segment string
	// ID is the value of the attribute that the rule tests, as the rule reads it.
	ID string
	// State is the answer: Unknown until the provider sets it.
	State State
}

// Provider answers the membership questions of one evaluation.
//
// Contains sets the State of every lookup it can answer, in place, and leaves the others Unknown. A lookup that
// cannot be answered is not an error: the engine applies the failure policy of the segment to it. A non-nil error
// means the call failed (a timeout, a broken connection, the end of the request context during the call): the
// lookups that it answered with Member or NotMember before it failed keep their answer, and the others are Unknown.
// Contains must honour ctx, must not retain lookups after it returns, must not change Segment or ID, and is called by
// concurrent evaluations: it must be safe for concurrent use. The slice is one per call and can have several lookups
// of the same segment, so a store can answer them in one round trip.
type Provider interface {
	Contains(ctx context.Context, lookups []Lookup) error
}

// Type is the kind of store of the members of a segment.
type Type string

const (
	// TypeFile is a segment whose members are loaded in memory from a file (one value per line).
	TypeFile Type = "file"
	// TypeRedis is a segment looked up in Redis or Valkey for each request, through go-redis (package redissegment).
	TypeRedis Type = "redis"
	// TypeValkey is a segment looked up in Valkey for each request, through valkey-go (package valkeysegment).
	TypeValkey Type = "valkey"
)

// OnError is what a lookup of a segment that is Unknown becomes, see Definition.OnError.
type OnError string

const (
	// OnErrorUnknown keeps the lookup Unknown (the opt-in of the three-valued logic): the rule that depends on it does
	// not match, unless the rule says onError: match. It is not the default: the default is OnErrorNotMember.
	OnErrorUnknown OnError = "unknown"
	// OnErrorNotMember turns an Unknown lookup into NotMember. It is the default, as in LaunchDarkly.
	OnErrorNotMember OnError = "notMember"
	// OnErrorMember turns an Unknown lookup into Member.
	OnErrorMember OnError = "member"
)

var (
	// ErrInvalidDefinition is wrapped by the errors of the definitions that are not valid.
	ErrInvalidDefinition = errors.New("invalid segment definition")
	errInvalidName       = errors.New("invalid name: use 1 to 64 characters of a-z, 0-9, _ and -")
	errInvalidType       = errors.New("invalid type: want file, redis or valkey")
	errInvalidOnError    = errors.New("invalid onError: want unknown, notMember or member")
	errDuplicate         = errors.New("duplicated name")
)

// Definition is a segment: its name, where its members are and what an unanswered lookup becomes. It holds no
// member and no connection detail; the provider that serves the segment holds those.
type Definition struct {
	// Version says how the version of the segment is learned. Nil means no version: only the time-to-live of a
	// cache applies.
	Version *VersionSpec
	// Name is the name that the rules use: segment("name").
	Name string
	// Type is the kind of store of the members.
	Type Type
	// OnError is what a lookup that the provider could not answer becomes. Empty means OnErrorNotMember.
	OnError OnError
	// Mode is how a segment that lives in a store is served. Empty means ModeSnapshot.
	Mode Mode
	// MissingKey is what a segment that does not exist in the store means. Empty means MissingNotMember.
	MissingKey MissingKey
	// Layout is how a store of keys holds the members. Empty means LayoutSet; LayoutKeys is lookup-only.
	Layout Layout
}

// Validate returns an error that wraps ErrInvalidDefinition when the definition is not valid.
func (d *Definition) Validate() error {
	if !ValidName(d.Name) {
		return fmt.Errorf("%w: %q: %w", ErrInvalidDefinition, d.Name, errInvalidName)
	}

	if d.Type != TypeFile && d.Type != TypeRedis && d.Type != TypeValkey {
		return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidDefinition, d.Name, errInvalidType, d.Type)
	}

	switch d.OnError {
	case "", OnErrorUnknown, OnErrorNotMember, OnErrorMember:
		return d.validateModeVersion()
	default:
		return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidDefinition, d.Name, errInvalidOnError, d.OnError)
	}
}

// Resolve applies the failure policy of the definition to a lookup state: an Unknown state becomes what OnError says.
func (d *Definition) Resolve(state State) State {
	if state != Unknown {
		return state
	}

	switch d.OnError {
	case "", OnErrorNotMember:
		return NotMember // the default, as in LaunchDarkly: what cannot be answered is not a member
	case OnErrorMember:
		return Member
	case OnErrorUnknown:
		return Unknown
	}

	return Unknown
}

// Catalog is the set of the segments that a flag set defines. It is immutable and safe for concurrent use: a change
// of the definitions is a new Catalog.
type Catalog struct {
	defs   map[string]Definition
	static map[string]*MemberSet
}

// NewCatalogWithSets is [NewCatalog] with the members of the static segments: the segment name of a set is answered
// from memory (see [Catalog.Member]), no provider is asked. The catalog keeps the sets, it does not copy them. A set
// whose name has no definition is an error.
func NewCatalogWithSets(sets map[string]*MemberSet, defs ...Definition) (*Catalog, error) {
	catalog, err := NewCatalog(defs...)
	if err != nil {
		return nil, err
	}

	for name := range sets {
		if _, ok := catalog.defs[name]; !ok {
			return nil, fmt.Errorf("%w: %q: members of a segment that is not defined", ErrInvalidDefinition, name)
		}
	}

	catalog.static = sets

	return catalog, nil
}

// NewCatalog validates the definitions and returns the catalog of them. The names must be unique.
func NewCatalog(defs ...Definition) (*Catalog, error) {
	catalog := &Catalog{defs: make(map[string]Definition, len(defs))}

	for _, def := range defs {
		def.Version = def.Version.clone() // the catalog owns its copy: the caller's spec stays the caller's

		err := def.Validate()
		if err != nil {
			return nil, err
		}

		if _, ok := catalog.defs[def.Name]; ok {
			return nil, fmt.Errorf("%w: %q: %w", ErrInvalidDefinition, def.Name, errDuplicate)
		}

		catalog.defs[def.Name] = def
	}

	return catalog, nil
}

// Has reports whether the catalog defines the segment name. It does not copy the definition: it is for the evaluation
// path.
func (c *Catalog) Has(name string) bool {
	if c == nil {
		return false
	}

	_, ok := c.defs[name]

	return ok
}

// Member answers a lookup from the members of a static segment: ok is false when the segment is not static, and
// the provider decides. It does not allocate and is safe for concurrent use.
func (c *Catalog) Member(name, id string) (State, bool) {
	if c == nil {
		return Unknown, false
	}

	set, ok := c.static[name]
	if !ok {
		return Unknown, false
	}

	if set.Contains(id) {
		return Member, true
	}

	return NotMember, true
}

// ResolveFor applies the failure policy of the segment name to a lookup state, as [Definition.Resolve] does; a segment
// that the catalog does not define (or no catalog) gets the default policy, not a member. It does not copy the
// definition: it is for the evaluation path.
func (c *Catalog) ResolveFor(name string, state State) State {
	if state != Unknown {
		return state
	}

	if c != nil {
		if def, ok := c.defs[name]; ok {
			return def.Resolve(state)
		}
	}

	return NotMember // a segment that is not defined has no member: the default policy, as in LaunchDarkly
}

// Lookup returns the definition of the segment name.
func (c *Catalog) Lookup(name string) (Definition, bool) {
	if c == nil {
		return Definition{}, false
	}

	def, ok := c.defs[name]
	def.Version = def.Version.clone() // a copy: the caller cannot change the catalog through it

	return def, ok
}

// Names returns the names of the segments, sorted.
func (c *Catalog) Names() []string {
	if c == nil {
		return nil
	}

	names := make([]string, 0, len(c.defs))
	for name := range c.defs {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}

// Status tells how healthy the segment lookups of an evaluation were. The worst status of the lookups wins.
type Status uint8

const (
	// StatusNone means the evaluation did not look any segment up.
	StatusNone Status = iota
	// StatusHealthy means every lookup was answered.
	StatusHealthy
	// StatusStale means the answers were given from data that is older than it should be (a snapshot that could not be
	// refreshed): they are used, the way a last known good set is.
	StatusStale
	// StatusNotConfigured means a rule used a segment but the evaluation had no provider.
	StatusNotConfigured
	// StatusStoreError means a lookup could not be answered by the provider.
	StatusStoreError
)

// String implements [fmt.Stringer] with the values of the evaluation metadata.
func (s Status) String() string {
	switch s {
	case StatusHealthy:
		return "HEALTHY"
	case StatusStale:
		return "STALE"
	case StatusNotConfigured:
		return "NOT_CONFIGURED"
	case StatusStoreError:
		return "STORE_ERROR"
	case StatusNone:
		return ""
	}

	return ""
}

// Reported reports whether the status is worth telling the caller: a segment was looked up and the lookup was not
// healthy. A healthy evaluation, and one that looked at no segment, report nothing.
func (s Status) Reported() bool {
	return s > StatusHealthy
}

// Worse returns the worse of the two statuses.
func (s Status) Worse(other Status) Status {
	return max(s, other)
}
