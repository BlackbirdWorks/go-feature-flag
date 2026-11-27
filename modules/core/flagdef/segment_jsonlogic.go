package flagdef

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/diegoholiveira/jsonlogic/v3"

	"github.com/blackbirdworks/semaphore/rules/parser"
)

// The inSegment operation of the JsonLogic queries:
//
//	{"inSegment": [{"var": "targetingKey"}, "beta-users"]}
//
// The first operand is the attribute, {"var": "name"} or {"var": "a.b"}, the second the name of the segment, a string
// literal. The operation is only valid where its three-valued result can be combined: as the query itself or as an
// operand of and, or, ! and !!. Anywhere else (an argument of another operation, in particular of an iterator such as
// map, filter, reduce, all, some and none, where it would run once for each element) the query is not valid. The
// rest of the query is evaluated by the JsonLogic library, as a leaf that is true or false.

const (
	opInSegment   = "inSegment"
	opAnd         = "and"
	opOr          = "or"
	opNot         = "!"
	opNotNot      = "!!"
	inSegmentArgs = 2
)

// ErrInvalidSegment is wrapped by the errors of a segment term that is not valid, in both query formats.
var ErrInvalidSegment = parser.ErrSegmentSyntax

// SegmentQueryError is the error of an inSegment operation that is not valid. It wraps ErrInvalidSegment.
type SegmentQueryError struct {
	// Path is the place of the problem in the query, such as $.and[1].or[0].
	Path string
	// Reason says what is wrong.
	Reason string
}

// Error implements error.
func (e *SegmentQueryError) Error() string {
	return fmt.Sprintf("%s at %s: %s", ErrInvalidSegment.Error(), e.Path, e.Reason)
}

// Unwrap returns ErrInvalidSegment.
func (*SegmentQueryError) Unwrap() error { return ErrInvalidSegment }

type jlKind uint8

const (
	jlLeaf jlKind = iota
	jlTerm
	jlAnd
	jlOr
	jlNot
	jlTruthy
)

// jlNode is a node of the logical skeleton of a JsonLogic query that has segment terms. It is immutable.
type jlNode struct {
	leaf     any
	children []*jlNode
	term     int
	kind     jlKind
}

// jlSegments is the compiled form of a JsonLogic query that has inSegment operations.
type jlSegments struct {
	root  *jlNode
	terms []parser.SegmentTerm
}

type jlCompiler struct {
	index map[string]int
	terms []parser.SegmentTerm
}

// compileJSONLogicSegments compiles the query, which is decoded JSON. It returns nil when the query has no
// inSegment operation.
func compileJSONLogicSegments(query any) (*jlSegments, error) {
	if !hasInSegment(query) {
		return nil, nil //nolint:nilnil // nil means the query has no segment term
	}

	comp := &jlCompiler{index: map[string]int{}}

	root, err := comp.node(query, "$")
	if err != nil {
		return nil, err
	}

	return &jlSegments{root: root, terms: comp.terms}, nil
}

// hasInSegment reports whether the operation inSegment is somewhere in the query.
func hasInSegment(node any) bool {
	switch val := node.(type) {
	case map[string]any:
		for key, arg := range val {
			if key == opInSegment || hasInSegment(arg) {
				return true
			}
		}
	case []any:
		return slices.ContainsFunc(val, hasInSegment)
	}

	return false
}

func (c *jlCompiler) node(query any, path string) (*jlNode, error) {
	obj, ok := query.(map[string]any)
	if !ok || len(obj) != 1 {
		return c.leaf(query, path, "")
	}

	var (
		operation string
		args      any
	)

	for key, value := range obj { // one entry
		operation, args = key, value
	}

	opPath := path + "." + operation

	switch operation {
	case opInSegment:
		return c.term(args, opPath)
	case opAnd, opOr:
		return c.logical(operation, args, opPath)
	case opNot, opNotNot:
		return c.negation(operation, args, opPath)
	default:
		return c.leaf(query, path, operation)
	}
}

func (*jlCompiler) leaf(node any, path, operation string) (*jlNode, error) {
	if hasInSegment(node) {
		where := "an operand that is not and, or, ! or !!"
		if operation != "" {
			where = fmt.Sprintf("the operation %q", operation)
		}

		return nil, &SegmentQueryError{
			Path:   path,
			Reason: opInSegment + " is only allowed as the query or as an operand of and, or, ! and !!, not inside " + where,
		}
	}

	// the library does not know inSegment, so it validates the leaves one by one
	if obj, ok := node.(map[string]any); ok {
		raw, err := json.Marshal(obj)
		if err != nil || !jsonlogic.IsValid(bytes.NewReader(raw)) {
			return nil, fmt.Errorf("%w at %s", errInvalidJsonlogicQuery, path)
		}
	}

	return &jlNode{kind: jlLeaf, leaf: node}, nil
}

func (c *jlCompiler) term(args any, path string) (*jlNode, error) {
	list, ok := args.([]any)
	if !ok || len(list) != inSegmentArgs {
		return nil, &SegmentQueryError{Path: path, Reason: `want two operands: [{"var": "attribute"}, "segment-name"]`}
	}

	attr, ok := list[0].(map[string]any)
	if !ok || len(attr) != 1 {
		return nil, &SegmentQueryError{Path: path + "[0]", Reason: `the attribute must be {"var": "name"}`}
	}

	name, ok := attr["var"].(string)
	if !ok || name == "" {
		return nil, &SegmentQueryError{Path: path + "[0]", Reason: `the attribute must be {"var": "name"} with a name`}
	}

	segName, ok := list[1].(string)
	if !ok {
		return nil, &SegmentQueryError{Path: path + "[1]", Reason: "the segment name must be a string literal"}
	}

	if !parser.ValidSegmentName(segName) {
		return nil, &SegmentQueryError{
			Path: path + "[1]",
			Reason: fmt.Sprintf("invalid segment name %q: use 1 to %d characters of a-z, 0-9, _ and -", segName,
				parser.MaxSegmentNameLen),
		}
	}

	attrPath := strings.Split(name, ".")
	key := segName + "\x00" + name

	idx, seen := c.index[key]
	if !seen {
		if len(c.terms) >= parser.MaxSegmentTerms {
			return nil, &SegmentQueryError{
				Path:   path,
				Reason: fmt.Sprintf("at most %d segment terms in a query", parser.MaxSegmentTerms),
			}
		}

		idx = len(c.terms)
		c.index[key] = idx
		c.terms = append(c.terms, parser.SegmentTerm{Name: segName, Path: attrPath})
	}

	return &jlNode{kind: jlTerm, term: idx}, nil
}

func (c *jlCompiler) logical(operation string, args any, path string) (*jlNode, error) {
	list, ok := args.([]any)
	if !ok || len(list) == 0 {
		return nil, &SegmentQueryError{Path: path, Reason: operation + " needs a non-empty list of operands"}
	}

	out := &jlNode{kind: jlAnd}
	if operation == opOr {
		out.kind = jlOr
	}

	for i, arg := range list {
		child, err := c.node(arg, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, err
		}

		out.children = append(out.children, child)
	}

	return out, nil
}

func (c *jlCompiler) negation(operation string, args any, path string) (*jlNode, error) {
	operand := args
	if list, ok := args.([]any); ok {
		if len(list) != 1 {
			return nil, &SegmentQueryError{Path: path, Reason: operation + " takes one operand"}
		}

		operand = list[0]
		path += "[0]"
	}

	child, err := c.node(operand, path)
	if err != nil {
		return nil, err
	}

	kind := jlNot
	if operation == opNotNot {
		kind = jlTruthy
	}

	return &jlNode{kind: kind, children: []*jlNode{child}}, nil
}

// evaluateJSONLogicSegments is evaluateRule for a JsonLogic query that has inSegment operations.
//
//nolint:nonamedreturns // recover sets the result
func (r *Rule) evaluateJSONLogicSegments(compiled *compiledQuery, queryCtx *queryContext) (success bool) {
	query := compiled.trimmed

	defer func() {
		if rec := recover(); rec != nil {
			queryCtx.svc.logger.LogAttrs(
				queryCtx.logContext(),
				slog.LevelError,
				"panic recovered while evaluating query",
				slog.Any("panic", rec),
				slog.String(queryName, query),
			)

			success = false
		}
	}()

	seg := compiled.jlSeg

	var buf [statesInline]parser.SegmentState

	states := buf[:0]
	if len(seg.terms) > len(buf) {
		states = make([]parser.SegmentState, 0, len(seg.terms))
	}

	states = states[:len(seg.terms)]

	match, err := queryCtx.resolveSegments(seg.terms, states, nil, seg.root)
	if err != nil {
		queryCtx.svc.logger.LogAttrs(
			queryCtx.logContext(),
			slog.LevelError,
			"error while evaluating the jsonlogic query",
			slog.String(queryName, query),
			slog.Any(errorLiteral, err.Error()),
		)

		return false
	}

	return r.matches(match)
}

// plain is the first pass of a JsonLogic rule with segment terms (see queryContext.resolveSegments): two valued, left
// to right, with the short circuit of "and" and "or", until the first segment term (reached).
func (n *jlNode) plain(queryCtx *queryContext) (bool, bool, error) {
	switch n.kind {
	case jlTerm:
		return false, true, nil
	case jlAnd, jlOr:
		decisive := n.kind == jlOr // true decides an or, false an and

		for _, child := range n.children {
			value, reached, err := child.plain(queryCtx)
			if err != nil || reached {
				return false, reached, err
			}

			if value == decisive {
				return decisive, false, nil
			}
		}

		return !decisive, false, nil
	case jlNot, jlTruthy:
		value, reached, err := n.children[0].plain(queryCtx)
		if err != nil || reached {
			return false, reached, err
		}

		return value == (n.kind == jlTruthy), false, nil
	case jlLeaf:
		match, err := n.matchLeaf(queryCtx)

		return match == parser.MatchYes, false, err
	}

	return false, false, nil
}

// match is the second pass: the whole rule, left to right, three-valued, with the answers. The operands of "and" and
// "or" after the one that decides the result are not evaluated; an unknown operand does not decide it.
func (n *jlNode) match(queryCtx *queryContext, states []parser.SegmentState) (parser.Match, error) {
	switch n.kind {
	case jlTerm:
		return states[n.term].Match(), nil
	case jlAnd, jlOr:
		return n.matchLogical(queryCtx, states)
	case jlNot, jlTruthy:
		child, err := n.children[0].match(queryCtx, states)
		if err != nil || n.kind == jlTruthy {
			return child, err
		}

		return child.Not(), nil
	case jlLeaf:
		return n.matchLeaf(queryCtx)
	}

	return parser.MatchUnknown, nil
}

// matchLeaf evaluates a part of the query that has no segment term with the JsonLogic library.
func (n *jlNode) matchLeaf(queryCtx *queryContext) (parser.Match, error) {
	data, err := queryCtx.dataOnce()
	if err != nil {
		return parser.MatchUnknown, err
	}

	result, err := jsonlogic.ApplyInterface(n.leaf, data)
	if err != nil {
		return parser.MatchUnknown, fmt.Errorf("applying the jsonlogic rule: %w", err)
	}

	if isJSONLogicTruthy(result) {
		return parser.MatchYes, nil
	}

	return parser.MatchNo, nil
}

func (n *jlNode) matchLogical(queryCtx *queryContext, states []parser.SegmentState) (parser.Match, error) {
	decisive, neutral := parser.MatchNo, parser.MatchYes // and
	if n.kind == jlOr {
		decisive, neutral = parser.MatchYes, parser.MatchNo
	}

	result := neutral

	for _, child := range n.children {
		match, err := child.match(queryCtx, states)
		if err != nil {
			return parser.MatchUnknown, err
		}

		if match == decisive {
			return decisive, nil
		}

		if match == parser.MatchUnknown {
			result = parser.MatchUnknown
		}
	}

	return result, nil
}
