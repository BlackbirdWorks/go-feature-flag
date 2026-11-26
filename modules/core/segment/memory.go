package segment

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// MemoryProvider is a Provider of segments whose members are held in memory, as a hash set per segment. It serves
// the segments of type file and the tests. It is immutable and safe for concurrent use: a change of the members is
// a new MemoryProvider. It does not answer a lookup of a segment it does not hold (the state stays Unknown).
type MemoryProvider struct {
	sets map[string]map[string]struct{}
}

// NewMemoryProvider returns a provider of the given members, indexed by segment name. The slices are copied.
func NewMemoryProvider(members map[string][]string) *MemoryProvider {
	sets := make(map[string]map[string]struct{}, len(members))

	for name, ids := range members {
		set := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			set[id] = struct{}{}
		}

		sets[name] = set
	}

	return &MemoryProvider{sets: sets}
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

		if _, member := set[lookups[i].ID]; member {
			lookups[i].State = Member
		} else {
			lookups[i].State = NotMember
		}
	}

	return nil
}

// ErrMemberTooLong is returned by ReadMembers for a line that is too long to be an identifier.
var ErrMemberTooLong = errors.New("a member line is longer than 4096 bytes")

const (
	maxMemberLen     = 4096
	initialMemberBuf = 512
)

// ReadMembers reads the members of a segment of type file: one value per line. Leading and trailing blanks are
// removed and empty lines are ignored; nothing else is interpreted.
func ReadMembers(r io.Reader) ([]string, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, initialMemberBuf), maxMemberLen)

	var members []string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			members = append(members, line)
		}
	}

	err := scanner.Err()
	if errors.Is(err, bufio.ErrTooLong) {
		return nil, ErrMemberTooLong
	}

	if err != nil {
		return nil, fmt.Errorf("reading the members: %w", err)
	}

	return members, nil
}
