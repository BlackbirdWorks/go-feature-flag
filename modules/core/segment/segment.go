package segment

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/blackbirdworks/semaphore/rules/parser"
)

// State is what a Provider knows of one lookup. The zero value is Unknown.
type State = parser.SegmentState

const (
	// Unknown means the provider could not tell: the store is down, the lookup timed out, the segment is not loaded.
	Unknown = parser.SegmentUnknown
	// NotMember means the value is not in the segment.
	NotMember = parser.SegmentNotMember
	// Member means the value is in the segment.
	Member = parser.SegmentMember
)

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
// means the call failed as a whole (a timeout, a broken connection): the engine ignores the states then and treats
// every lookup as Unknown. Contains must honour ctx, must not retain lookups after it returns, must not change
// Segment or ID, and is called by concurrent evaluations: it must be safe for concurrent use. The slice is one
// per call and can have several lookups of the same segment, so a store can answer them in one round trip.
type Provider interface {
	Contains(ctx context.Context, lookups []Lookup) error
}

// Type is the kind of store of the members of a segment.
type Type string

const (
	// TypeFile is a segment whose members are loaded in memory from a file (one value per line).
	TypeFile Type = "file"
	// TypeValkey is a segment looked up in Valkey for each request.
	TypeValkey Type = "valkey"
)

// OnError is what a lookup of a segment that is Unknown becomes, see Definition.OnError.
type OnError string

const (
	// OnErrorUnknown keeps the lookup Unknown: the rule that depends on it does not match, unless the rule says
	// onError: match. It is the default.
	OnErrorUnknown OnError = "unknown"
	// OnErrorNotMember turns an Unknown lookup into NotMember.
	OnErrorNotMember OnError = "notMember"
	// OnErrorMember turns an Unknown lookup into Member.
	OnErrorMember OnError = "member"
)

var (
	// ErrInvalidDefinition is wrapped by the errors of the definitions that are not valid.
	ErrInvalidDefinition = errors.New("invalid segment definition")
	errInvalidName       = errors.New("invalid name: use 1 to 64 characters of a-z, 0-9, _ and -")
	errInvalidType       = errors.New("invalid type: want file or valkey")
	errInvalidOnError    = errors.New("invalid onError: want unknown, notMember or member")
	errDuplicate         = errors.New("duplicated name")
)

// Definition is a segment: its name, where its members are and what an unanswered lookup becomes. It holds no
// member and no connection detail; the provider that serves the segment holds those.
type Definition struct {
	// Name is the name that the rules use: segment("name").
	Name string
	// Type is the kind of store of the members.
	Type Type
	// OnError is what a lookup that the provider could not answer becomes. Empty means OnErrorUnknown.
	OnError OnError
}

// Validate returns an error that wraps ErrInvalidDefinition when the definition is not valid.
func (d Definition) Validate() error {
	if !parser.ValidSegmentName(d.Name) {
		return fmt.Errorf("%w: %q: %w", ErrInvalidDefinition, d.Name, errInvalidName)
	}

	if d.Type != TypeFile && d.Type != TypeValkey {
		return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidDefinition, d.Name, errInvalidType, d.Type)
	}

	switch d.OnError {
	case "", OnErrorUnknown, OnErrorNotMember, OnErrorMember:
		return nil
	default:
		return fmt.Errorf("%w: %q: %w, got %q", ErrInvalidDefinition, d.Name, errInvalidOnError, d.OnError)
	}
}

// Resolve applies the failure policy of the definition to a lookup state: an Unknown state becomes what OnError says.
func (d Definition) Resolve(state State) State {
	if state != Unknown {
		return state
	}

	switch d.OnError {
	case OnErrorNotMember:
		return NotMember
	case OnErrorMember:
		return Member
	case "", OnErrorUnknown:
		return Unknown
	}

	return Unknown
}

// Catalog is the set of the segments that a flag set defines. It is immutable and safe for concurrent use: a change
// of the definitions is a new Catalog.
type Catalog struct {
	defs map[string]Definition
}

// NewCatalog validates the definitions and returns the catalog of them. The names must be unique.
func NewCatalog(defs ...Definition) (*Catalog, error) {
	catalog := &Catalog{defs: make(map[string]Definition, len(defs))}

	for _, def := range defs {
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

// Lookup returns the definition of the segment name.
func (c *Catalog) Lookup(name string) (Definition, bool) {
	if c == nil {
		return Definition{}, false
	}

	def, ok := c.defs[name]

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
	case StatusNotConfigured:
		return "NOT_CONFIGURED"
	case StatusStoreError:
		return "STORE_ERROR"
	case StatusNone:
		return ""
	}

	return ""
}

// Worse returns the worse of the two statuses.
func (s Status) Worse(other Status) Status {
	return max(s, other)
}
