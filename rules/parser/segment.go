package parser

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The segment() term.
//
// A rule can test an attribute against a named segment:
//
//	targetingKey in segment("beta-users") and not (email in segment("churned"))
//
// The ANTLR grammar of the rules is generated and has no such term, so before the rule is parsed every
// segment("name") call is replaced by a string literal of the same length: the position of the errors of the
// rest of the rule do not move. The literal starts with segmentMarker, then the name, then padding. When the parse
// tree is compiled, the comparison "<attribute> in <marker literal>" becomes a native segment node: it has no
// string operand and reads the membership of the request from a vector, see ProcessSegments.

const (
	// segmentKeyword is the name of the function in the rules.
	segmentKeyword = "segment"
	// segmentMarker starts the literal that replaces a segment() call, segmentPad fills the rest of it.
	segmentMarker = '\x01'
	segmentPad    = '\x02'
	// MaxSegmentNameLen is the longest name of a segment.
	MaxSegmentNameLen = 64
	// MaxSegmentTerms is the most segment terms (distinct attribute and segment pairs) of one rule.
	MaxSegmentTerms = 32

	// markerOverhead is the length of the literal that replaces a call, without the name and the padding: two
	// quotes and the marker. A call is longer than that: segment("") is 12 bytes.
	markerOverhead = 3
	// reservedBytes are the characters a rule that calls segment() must not contain.
	reservedBytes = "\x01\x02"
)

var (
	// ErrSegmentSyntax is wrapped by the errors of a segment() term that is not valid.
	ErrSegmentSyntax = errors.New("invalid segment() term")
	// ErrSegmentsNotResolved is returned by Process for a rule that has segment terms: such a rule can only be
	// evaluated with ProcessSegments, because the membership of the attributes is an input of the evaluation.
	ErrSegmentsNotResolved = errors.New("the rule has segment() terms: evaluate it with ProcessSegments")
	// ErrSegmentStates is returned by ProcessSegments when it is not given one state for each segment term.
	ErrSegmentStates = errors.New("one segment state is needed for each segment term")
	errTooManyTerms  = fmt.Errorf("%w: too many segment() terms in the rule", ErrSegmentSyntax)
	errNotCompiled   = errors.New("the rule could not be compiled")
)

// SegmentError is the error of a segment() term that is not valid. It wraps ErrSegmentSyntax.
type SegmentError struct {
	// Reason says what is wrong, in a sentence.
	Reason string
	// Offset is the byte offset in the rule of the problem.
	Offset int
}

// Error implements error.
func (e *SegmentError) Error() string {
	return fmt.Sprintf("%s at offset %d: %s", ErrSegmentSyntax.Error(), e.Offset, e.Reason)
}

// Unwrap returns ErrSegmentSyntax.
func (*SegmentError) Unwrap() error { return ErrSegmentSyntax }

// ValidSegmentName reports whether name is a segment name: 1 to 64 characters of a-z, 0-9, "_" and "-".
func ValidSegmentName(name string) bool {
	if name == "" || len(name) > MaxSegmentNameLen {
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

// SegmentState is what is known of the membership of an attribute value in a segment.
// The zero value is SegmentUnknown, so a new vector reads as "nothing is known".
type SegmentState uint8

const (
	// SegmentUnknown means the membership could not be determined (store down, timeout, no provider).
	SegmentUnknown SegmentState = iota
	// SegmentNotMember means the value is not in the segment.
	SegmentNotMember
	// SegmentMember means the value is in the segment.
	SegmentMember
)

// String implements [fmt.Stringer].
func (s SegmentState) String() string {
	switch s {
	case SegmentNotMember:
		return "notMember"
	case SegmentMember:
		return "member"
	case SegmentUnknown:
		return "unknown"
	}

	return "SegmentState(" + strconv.Itoa(int(s)) + ")"
}

// Match is the three-valued result of a rule with segment terms.
type Match uint8

const (
	// MatchUnknown means the result depends on a segment term whose membership is unknown.
	MatchUnknown Match = iota
	// MatchNo means the rule does not match, whatever the unknown terms are.
	MatchNo
	// MatchYes means the rule matches, whatever the unknown terms are.
	MatchYes
)

// String implements [fmt.Stringer].
func (m Match) String() string {
	switch m {
	case MatchNo:
		return "no"
	case MatchYes:
		return "yes"
	case MatchUnknown:
		return "unknown"
	}

	return "Match(" + strconv.Itoa(int(m)) + ")"
}

// Match is the result of a term whose membership is the state.
func (s SegmentState) Match() Match {
	switch s {
	case SegmentMember:
		return MatchYes
	case SegmentNotMember:
		return MatchNo
	case SegmentUnknown:
		return MatchUnknown
	}

	return MatchUnknown
}

func matchOf(b bool) Match {
	if b {
		return MatchYes
	}

	return MatchNo
}

// Not is the three-valued negation: an unknown result stays unknown.
func (m Match) Not() Match {
	switch m {
	case MatchYes:
		return MatchNo
	case MatchNo:
		return MatchYes
	case MatchUnknown:
		return MatchUnknown
	}

	return MatchUnknown
}

// SegmentTerm is a segment term of a rule: the attribute whose value is looked up, in the segment Name.
// Two equal terms of a rule are one term.
type SegmentTerm struct {
	// Name is the segment.
	Name string
	// Path is the attribute, one element for each level of a nested attribute ("user.email" is {"user", "email"}).
	Path []string
}

// HasSegmentCall reports whether the rule calls segment(), validly or not. It is the test that the evaluator
// applies to decide that a rule has segment terms, so a word segment that is only an attribute does not count.
func HasSegmentCall(rule string) bool {
	_, has, err := scanSegments(rule)

	return has || err != nil
}

// scanSegments replaces the segment() calls of rule. It returns rule itself when it has none.
func scanSegments(rule string) (string, bool, error) {
	if !strings.Contains(rule, segmentKeyword) {
		return rule, false, nil
	}

	var out []byte

	last := 0

	for from := 0; ; {
		start := nextSegmentWord(rule, from)
		if start < 0 {
			break
		}

		end, name, err := parseSegmentCall(rule, start)
		if err != nil {
			return "", false, err
		}

		if out == nil {
			if at := strings.IndexAny(rule, reservedBytes); at >= 0 {
				return "", false, &SegmentError{
					Offset: at,
					Reason: "the control characters 0x01 and 0x02 are not allowed in a rule that uses segment()",
				}
			}

			out = make([]byte, 0, len(rule))
		}

		out = append(out, rule[last:start]...)
		out = appendMarkerLiteral(out, name, end-start)
		last = end
		from = end
	}

	if out == nil {
		return rule, false, nil
	}

	return string(append(out, rule[last:]...)), true, nil
}

// nextSegmentWord returns the offset of the first called word segment at or after from, outside the string literals
// of the rule, or -1. from must be outside a string literal.
func nextSegmentWord(rule string, from int) int {
	inString := false
	escape := false

	for i := from; i < len(rule); i++ {
		cur := rule[i]

		switch {
		case inString && escape:
			escape = false
		case inString && cur == '\\':
			escape = true
		case inString:
			inString = cur != '"'
		case cur == '"':
			inString = true
		case isSegmentWord(rule, i):
			return i
		}
	}

	return -1
}

func isIdentByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == ':' ||
		c == '.'
}

// isSegmentWord reports whether the word "segment" is at i, as a whole word that is called: followed, after blanks, by
// an opening parenthesis. A word "segment" that is not called is an attribute.
func isSegmentWord(rule string, i int) bool {
	if !strings.HasPrefix(rule[i:], segmentKeyword) {
		return false
	}

	if i > 0 && isIdentByte(rule[i-1]) {
		return false
	}

	after := skipBlanks(rule, i+len(segmentKeyword))

	return after < len(rule) && rule[after] == '('
}

func skipBlanks(rule string, i int) int {
	for i < len(rule) && (rule[i] == ' ' || rule[i] == '\t' || rule[i] == '\n' || rule[i] == '\r') {
		i++
	}

	return i
}

// parseSegmentCall parses the call that starts at start (the word segment) and returns the offset after it and
// the name. The call is only valid as the right side of "in".
func parseSegmentCall(rule string, start int) (int, string, error) {
	if !followsIn(rule, start) {
		return 0, "", &SegmentError{
			Offset: start,
			Reason: `segment() is only valid as the right side of "in": <attribute> in segment("name")`,
		}
	}

	pos := skipBlanks(rule, start+len(segmentKeyword)) // at "("

	pos = skipBlanks(rule, pos+1)
	if pos >= len(rule) || rule[pos] != '"' {
		return 0, "", &SegmentError{Offset: pos, Reason: `the segment name must be a string literal: segment("name")`}
	}

	nameStart := pos + 1

	nameEnd := strings.IndexByte(rule[nameStart:], '"')
	if nameEnd < 0 {
		return 0, "", &SegmentError{Offset: pos, Reason: "unterminated segment name"}
	}

	name := rule[nameStart : nameStart+nameEnd]
	if !ValidSegmentName(name) {
		return 0, "", &SegmentError{
			Offset: nameStart,
			Reason: fmt.Sprintf(
				"invalid segment name %q: use 1 to %d characters of a-z, 0-9, _ and -",
				name,
				MaxSegmentNameLen,
			),
		}
	}

	pos = skipBlanks(rule, nameStart+nameEnd+1)
	if pos >= len(rule) || rule[pos] != ')' {
		return 0, "", &SegmentError{Offset: pos, Reason: "segment() takes exactly one argument, the name"}
	}

	return pos + 1, name, nil
}

// followsIn reports whether the word before start is "in" (any case), as a whole word.
func followsIn(rule string, start int) bool {
	end := start

	for end > 0 && (rule[end-1] == ' ' || rule[end-1] == '\t' || rule[end-1] == '\n' || rule[end-1] == '\r') {
		end--
	}

	if end == start || end < len(`in`) {
		return false
	}

	word := rule[end-2 : end]
	if word != "in" && word != "IN" {
		return false
	}

	return end-2 == 0 || !isIdentByte(rule[end-3])
}

// appendMarkerLiteral appends the string literal that replaces a call of n bytes: it has n bytes too.
func appendMarkerLiteral(out []byte, name string, n int) []byte {
	out = append(out, '"', segmentMarker)
	out = append(out, name...)

	for pad := n - len(name) - markerOverhead; pad > 0; pad-- {
		out = append(out, segmentPad)
	}

	return append(out, '"')
}

// segmentCompiler collects the segment terms of a rule while its parse tree is compiled.
type segmentCompiler struct {
	err   error
	index map[string]int
	terms []SegmentTerm
}

// termFor returns the index of the term of the attribute path in the segment name.
func (c *segmentCompiler) termFor(name string, path []string) (int, error) {
	key := name + "\x00" + strings.Join(path, "\x00")
	if i, ok := c.index[key]; ok {
		return i, nil
	}

	if len(c.terms) >= MaxSegmentTerms {
		return 0, fmt.Errorf("%w: at most %d", errTooManyTerms, MaxSegmentTerms)
	}

	if c.index == nil {
		c.index = map[string]int{}
	}

	c.index[key] = len(c.terms)
	c.terms = append(c.terms, SegmentTerm{Name: name, Path: path})

	return len(c.terms) - 1, nil
}

// segmentName returns the segment of a literal that scanSegments wrote.
func segmentName(literal string) (string, bool) {
	if literal == "" || literal[0] != segmentMarker {
		return "", false
	}

	return strings.TrimRight(literal[1:], "\x02"), true
}
