package segment

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
)

// MemberSet is the members of one segment, as a hash set. It is immutable once built.
type MemberSet struct {
	set map[string]struct{}
}

// NewMemberSet returns the set of the given members (duplicates are one member). The slice is not retained.
func NewMemberSet(members []string) *MemberSet {
	set := make(map[string]struct{}, len(members))
	for _, member := range members {
		set[member] = struct{}{}
	}

	return &MemberSet{set: set}
}

// Len is the number of distinct members.
func (s *MemberSet) Len() int { return len(s.set) }

// Contains reports whether id is a member.
func (s *MemberSet) Contains(id string) bool {
	_, ok := s.set[id]

	return ok
}

// MemoryProvider is a Provider of segments whose members are held in memory, as a hash set per segment. It serves
// the segments of type file and the tests. It is immutable and safe for concurrent use: a change of the members is
// a new MemoryProvider. It does not answer a lookup of a segment it does not hold (the state stays Unknown).
type MemoryProvider struct {
	sets map[string]*MemberSet
}

// NewMemoryProvider returns a provider of the given members, indexed by segment name. The slices are copied.
func NewMemoryProvider(members map[string][]string) *MemoryProvider {
	sets := make(map[string]*MemberSet, len(members))
	for name, ids := range members {
		sets[name] = NewMemberSet(ids)
	}

	return &MemoryProvider{sets: sets}
}

// NewMemoryProviderFromSets returns a provider of the given sets, indexed by segment name. The sets are shared, not
// copied: they are immutable.
func NewMemoryProviderFromSets(sets map[string]*MemberSet) *MemoryProvider {
	return &MemoryProvider{sets: maps.Clone(sets)}
}

// Contains implements Provider. It does not allocate.
func (p *MemoryProvider) Contains(ctx context.Context, lookups []Lookup) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("segment lookup: %w", err)
	}

	for i := range lookups {
		set, ok := p.sets[lookups[i].Segment]
		if !ok {
			continue
		}

		if set.Contains(lookups[i].ID) {
			lookups[i].State = Member
		} else {
			lookups[i].State = NotMember
		}
	}

	return nil
}

var (
	// ErrMemberTooLong is returned by ReadMemberSet for a line that is too long to be an identifier.
	ErrMemberTooLong = errors.New("a member line is longer than 4096 bytes")
	// ErrTooManyMembers is returned by ReadMemberSet when the file has more rows than Limits.MaxMembers.
	ErrTooManyMembers = errors.New("the member file has too many rows")
	// ErrMembersTooLarge is returned by ReadMemberSet when the file is larger than Limits.MaxBytes.
	ErrMembersTooLarge = errors.New("the member file is too large")
)

const (
	maxMemberLen     = 4096
	lineEndLen       = 2 // CRLF
	initialMemberBuf = 512

	// DefaultMaxMembers is the default Limits.MaxMembers.
	DefaultMaxMembers = 10_000_000
	// DefaultMaxBytes is the default Limits.MaxBytes.
	DefaultMaxBytes = 512 << 20
)

// Limits bounds what ReadMemberSet reads. A zero field means its default.
type Limits struct {
	// MaxMembers is the most non-empty rows of the file (duplicates count).
	MaxMembers int
	// MaxBytes is the most bytes of the file, line ends included.
	MaxBytes int64
}

// ReadMemberSet reads the members of a segment of type file, one value per line, straight into the set (no list of
// the members is built). A UTF-8 byte order mark at the start is ignored; leading and trailing blanks (including a
// carriage return) are removed, empty lines are ignored, duplicates are one member, and nothing else is interpreted.
func ReadMemberSet(r io.Reader, limits Limits) (*MemberSet, error) {
	if limits.MaxMembers <= 0 {
		limits.MaxMembers = DefaultMaxMembers
	}

	if limits.MaxBytes <= 0 {
		limits.MaxBytes = DefaultMaxBytes
	}

	counted := &countingReader{reader: r, limit: limits.MaxBytes}

	scanner := bufio.NewScanner(counted)
	// the buffer holds a member of the longest length and its line end (LF or CRLF)
	scanner.Buffer(make([]byte, 0, initialMemberBuf), maxMemberLen+lineEndLen)

	set := &MemberSet{set: map[string]struct{}{}}
	rows := 0

	first := true

	for scanner.Scan() {
		line := scanner.Bytes()

		if first {
			line = bytes.TrimPrefix(line, []byte("\xEF\xBB\xBF")) // the UTF-8 byte order mark
			first = false
		}

		member := strings.TrimSpace(string(line))
		if member == "" {
			continue
		}

		if len(member) > maxMemberLen {
			return nil, ErrMemberTooLong
		}

		rows++
		if rows > limits.MaxMembers {
			return nil, fmt.Errorf("%w: more than %d", ErrTooManyMembers, limits.MaxMembers)
		}

		set.set[member] = struct{}{}
	}

	err := scanner.Err()
	if errors.Is(err, ErrMembersTooLarge) {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrMembersTooLarge, limits.MaxBytes)
	}

	if errors.Is(err, bufio.ErrTooLong) {
		return nil, ErrMemberTooLong
	}

	if err != nil {
		return nil, fmt.Errorf("reading the members: %w", err)
	}

	return set, nil
}

// countingReader counts the bytes that it reads and fails with ErrMembersTooLarge once they are more than limit: the
// limit is the size of the file, whatever its line ends.
type countingReader struct {
	reader io.Reader
	read   int64
	limit  int64
}

func (c *countingReader) Read(buf []byte) (int, error) {
	got, err := c.reader.Read(buf)

	c.read += int64(got)
	if c.read > c.limit {
		return got, ErrMembersTooLarge
	}

	if err != nil {
		return got, err //nolint:wrapcheck // the reader of the caller: io.EOF must stay io.EOF
	}

	return got, nil
}
