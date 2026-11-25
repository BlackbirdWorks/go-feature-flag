package parser

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"github.com/blang/semver/v4"
)

const (
	// versionDots is the number of dots of a semantic version: the last number has no dot after it.
	versionDots = 2
)

type nodeKind int

// inSetMinLen is the list length from which IN uses a hash set instead of a
// linear scan. Below it the scan is as fast and the set only costs memory.
const inSetMinLen = 64

const (
	nodeParen nodeKind = iota
	nodeLogical
	nodePresent
	nodeCompare
)

type valueKind int

const (
	valueConst valueKind = iota
	valueInts
	valueDoubles
	valueStrings
)

// node is one immutable node of a rule compiled from the ANTLR parse tree.
// Everything that does not depend on the input object (operators, constants,
// attribute paths, IN lists) is resolved once by compileQuery so that eval
// never touches the parse tree. Paren and logical nodes only use the fields
// below; present and compare nodes are allocated as a compareNode holding
// the rest, so the common inner nodes stay small.
type node struct {
	left, right *node
	cmp         *compareNode // present / compare: the compareNode this node is part of
	kind        nodeKind
	// paren / logical
	not  bool // paren: negate the result
	isOr bool // logical: or (true) / and (false)
}

// compareNode is a present or compare node.
type compareNode struct {
	node

	listOp  Operand // lists: the parsed list ([]int, []float64 or []string)
	rightOp Operand // valueConst: operand set for the rule value
	valErr  error   // valueConst: error set on eval (invalid integer)
	listErr error   // lists: parse error of the first bad element; the list then holds the elements before it
	intSet  *intSet // long int lists: hash index over listOp, used by IN
	apply   func(Operand, Operand) (bool, error)
	version *versionCompare // set instead of using apply for a pre-parsed rule version
	strSet  *strSet         // long string lists: the packed list and its hash index, used by IN; listOp is nil then
	// compare
	attrPath string
	path     []string
	opType   int
	valKind  valueKind
}

func newCompareNode(kind nodeKind, path []string) *compareNode {
	c := &compareNode{path: path}
	c.kind = kind
	c.cmp = c

	return c
}

// evalState mirrors the mutable state the visitor keeps while walking a
// rule, including its quirks (stale leftOp/rightOp, the attribute stack).
type evalState struct {
	leftOp   Operand
	rightOp  Operand
	err      error
	debugErr error
	item     map[string]any
	stack    attrStack
}

// attrStack is the visitor's attribute stack. The first attrStackInline
// entries live in the struct itself so that, with evalState on the
// goroutine stack, nested paths do not allocate; deeper entries spill to
// more. Nothing takes the address of inline, which would move the whole
// evalState to the heap.
type attrStack struct {
	inline [attrStackInline]any
	more   []any
	n      int
}

const attrStackInline = 4

func (s *attrStack) push(v any) {
	if s.n < attrStackInline {
		s.inline[s.n] = v
	} else {
		s.more = append(s.more, v)
	}

	s.n++
}

// top returns the last pushed entry; s must not be empty.
func (s *attrStack) top() any {
	if s.n <= attrStackInline {
		return s.inline[s.n-1]
	}

	return s.more[s.n-1-attrStackInline]
}

// pop drops the last pushed entry; s must not be empty.
func (s *attrStack) pop() {
	s.n--
	if s.n >= attrStackInline {
		s.more = s.more[:s.n-attrStackInline]
	}
}

func (s *attrStack) clear() {
	s.n = 0
	s.more = s.more[:0]
}

func (st *evalState) resolve(path []string) {
	last := len(path) - 1
	for _, name := range path[:last] {
		var item any
		if st.stack.n > 0 {
			item = st.stack.top()
		} else {
			item = st.item
		}

		if item == nil {
			return
		}

		m := item.(map[string]any) //nolint:errcheck,forcetypeassert // panics on mismatch, as before
		st.stack.push(m[name])
	}

	var item any
	if st.stack.n > 0 {
		item = st.stack.top()
		st.stack.pop()
	} else {
		item = st.item
	}

	if item == nil {
		return
	}

	m := item.(map[string]any) //nolint:errcheck,forcetypeassert // panics on mismatch, as before
	st.leftOp = m[path[last]]
	st.stack.clear()
}

func (n *node) eval(state *evalState) bool {
	switch n.kind {
	case nodeParen:
		result := n.left.eval(state)
		if n.not {
			return !result
		}

		return result
	case nodeLogical:
		left := n.left.eval(state)
		if n.isOr {
			if left {
				return left
			}

			return n.right.eval(state)
		}

		if !left {
			return left
		}

		return n.right.eval(state)
	case nodePresent:
		state.resolve(n.cmp.path)

		return state.leftOp != nil
	case nodeCompare:
		return n.cmp.evalCompare(state)
	default:
		panic(fmt.Sprintf("unexpected node kind %d", n.kind))
	}
}

func (n *compareNode) setRight(state *evalState) {
	if n.valKind != valueConst && n.listErr == nil && state.rightOp == nil {
		state.rightOp = n.listValue()

		return
	}

	switch n.valKind {
	case valueInts:
		var list []int
		if state.rightOp == nil {
			list = []int{}
		} else {
			list = state.rightOp.([]int) //nolint:errcheck,forcetypeassert // panics on mismatch, as before
		}

		state.rightOp = append(list, n.listOp.([]int)...) //nolint:errcheck,forcetypeassert,gocritic // stale operand
		if n.listErr != nil {
			state.err = cloneErr(n.listErr)
		}
	case valueDoubles:
		var list []float64
		if state.rightOp == nil {
			list = []float64{}
		} else {
			list = state.rightOp.([]float64) //nolint:errcheck,forcetypeassert // panics on mismatch, as before
		}
		//nolint:errcheck,forcetypeassert,gocritic // stale operand
		state.rightOp = append(list, n.listOp.([]float64)...)
		if n.listErr != nil {
			state.err = cloneErr(n.listErr)
		}
	case valueStrings:
		var list []string
		if state.rightOp == nil {
			list = []string{}
		} else {
			list = state.rightOp.([]string) //nolint:errcheck,forcetypeassert // panics on mismatch, as before
		}
		//nolint:errcheck,forcetypeassert,gocritic // stale operand
		state.rightOp = append(list, n.listValue().([]string)...)
	default:
		state.rightOp = n.rightOp
		if n.valErr != nil {
			state.err = cloneErr(n.valErr)
		}
	}
}

func (n *compareNode) evalCompare(state *evalState) bool {
	state.resolve(n.path)

	if n.strSet != nil || n.intSet != nil {
		if r, ok := n.setIN(state); ok {
			return r
		}
	}

	n.setRight(state)

	if state.err != nil {
		return false
	}

	var (
		ret bool
		err error
	)
	if n.version != nil {
		ret, err = n.version.compare(state.leftOp)
	} else {
		ret, err = n.apply(state.leftOp, state.rightOp)
	}

	if err != nil {
		// operations return their errors unwrapped, so the exact type is checked; errors.As here costs
		// the evaluation path (measured), and this block is inlined on purpose.
		if eio, ok := err.(*InvalidOperandError); ok && n.valKind != valueConst { //nolint:errorlint // see setDebugErr
			// the operand may be the shared compiled list
			c := *eio
			c.Val = copyOperand(c.Val)
			c.typeObj = copyOperand(c.typeObj)
			err = &c
		}

		n.setDebugErr(state, err)

		ret = false
	}

	state.rightOp = nil

	return ret
}

// listValue returns the parsed list as an operand. A packed long string
// list is expanded into a fresh []string; that only happens off the hash
// lookup path (errors, stale operands, operators other than IN).
func (n *compareNode) listValue() Operand {
	if n.strSet != nil {
		return n.strSet.slice()
	}

	return n.listOp
}

// setIN answers IN for long compiled string/int lists with a hash lookup,
// before the list operand is materialized. It preserves the linear scan's
// semantics (exact, case-sensitive string equality; int left operand only).
// It applies only when nothing is pending in the evaluation state (no stale
// operand, no earlier error) and the left operand passes the same type check;
// otherwise it reports false and the regular linear path runs, producing the
// original errors and debug errors.
func (n *compareNode) setIN(state *evalState) (bool, bool) {
	if n.opType != JsonQueryParserIN || n.listErr != nil || n.version != nil || state.rightOp != nil ||
		state.err != nil {
		return false, false
	}

	switch {
	case n.strSet != nil:
		left, err := (&StringOperation{}).getString(state.leftOp)
		if err != nil {
			return false, false
		}

		return n.strSet.has(left), true
	case n.intSet != nil:
		left, ok := state.leftOp.(int)
		if !ok {
			return false, false
		}

		return n.intSet.has(left), true
	}

	return false, false
}

func (n *compareNode) setDebugErr(state *evalState, err error) {
	// Operations return these sentinels and *InvalidOperandError unwrapped; the exact comparisons are kept
	// because the wrapping-aware forms measurably slow the evaluation path.
	switch err {
	case ErrInvalidOperation: //nolint:errorlint // see above
		// in case of invalid operation lets rather
		// be conservative and return false because the rule doesn't even make
		// sense. It can be argued that it would be false positive if we were
		// to return true
		state.err = err
		state.debugErr = newNestedError(err, "Not a valid operation for datatypes").Set(ErrVals{
			errValOperation:     n.opType,
			errValObjectOperand: state.leftOp,
			errValRuleOperand:   copyOperand(state.rightOp),
		})
	case ErrEvalOperandMissing:
		state.debugErr = newNestedError(err, "Eval operand missing in input object").Set(ErrVals{
			errValAttrPath: n.attrPath,
		})
	default:
		msg := "unknown error"
		if _, ok := err.(*InvalidOperandError); ok { //nolint:errorlint // see above
			msg = "operands are not the right value type"
		}

		state.debugErr = newNestedError(err, msg).Set(ErrVals{
			errValAttrPath:      n.attrPath,
			errValObjectOperand: state.leftOp,
			errValRuleOperand:   copyOperand(state.rightOp),
		})
	}
}

// copyOperand copies compiled IN lists before they are exposed through a
// debug error so callers cannot mutate the shared rule constants.
func copyOperand(operand Operand) Operand {
	switch val := operand.(type) {
	case []int:
		return append(make([]int, 0, len(val)), val...)
	case []float64:
		return append(make([]float64, 0, len(val)), val...)
	case []string:
		return append(make([]string, 0, len(val)), val...)
	}

	return operand
}

// cloneErr returns a fresh copy of a pre-computed parse error so each call
// surfaces its own error value.
func cloneErr(err error) error {
	if ne, ok := err.(*strconv.NumError); ok { //nolint:errorlint // returned unwrapped by strconv
		c := *ne

		return &c
	}

	return err
}

func compileQuery(tree antlr.ParseTree) *node {
	return compileTree(tree, &sourceText{})
}

func compileTree(tree antlr.ParseTree, src *sourceText) *node {
	switch val := tree.(type) {
	case *ParenExpContext:
		return &node{kind: nodeParen, left: compileTree(val.Query(), src), not: val.NOT() != nil}
	case *LogicalExpContext:
		return &node{
			kind:  nodeLogical,
			left:  compileTree(val.Query(0), src),
			right: compileTree(val.Query(1), src),
			isOr:  val.LOGICAL_OPERATOR().GetText() == "or",
		}
	case *PresentExpContext:
		return &newCompareNode(nodePresent, compileAttrPath(val.AttrPath())).node
	case *CompareExpContext:
		cmpNode := newCompareNode(nodeCompare, compileAttrPath(val.AttrPath()))
		cmpNode.attrPath = val.AttrPath().GetText()
		cmpNode.opType = val.op.GetTokenType()
		operation := compileValue(cmpNode, val.Value(), src)

		cmpNode.apply = applyFor(operation, cmpNode.opType)
		if cmpNode.apply == nil {
			panic(fmt.Sprintf("no operation for operator token %d", cmpNode.opType))
		}

		if _, ok := operation.(*VersionOperation); ok {
			ruleVersion := cmpNode.rightOp.(string) //nolint:errcheck,forcetypeassert // panics on mismatch, as before
			cmpNode.version = compileVersionCompare(ruleVersion, cmpNode.opType)
		}

		return &cmpNode.node
	default:
		// unreachable with the grammar; compile recovers and falls back to
		// the visitor
		panic(fmt.Sprintf("unexpected parse tree node %T", tree))
	}
}

func compileAttrPath(ctx IAttrPathContext) []string {
	var path []string
	for {
		path = append(path, ctx.ATTRNAME().GetText())

		sub := ctx.SubAttr()
		if sub == nil || sub.IsEmpty() {
			return path
		}

		ctx = sub.AttrPath()
	}
}

func applyFor(operation Operation, opType int) func(Operand, Operand) (bool, error) {
	switch opType {
	case JsonQueryParserEQ:
		return operation.EQ
	case JsonQueryParserNE:
		return operation.NE
	case JsonQueryParserGT:
		return operation.GT
	case JsonQueryParserLT:
		return operation.LT
	case JsonQueryParserLE:
		return operation.LE
	case JsonQueryParserGE:
		return operation.GE
	case JsonQueryParserCO:
		return operation.CO
	case JsonQueryParserSW:
		return operation.SW
	case JsonQueryParserEW:
		return operation.EW
	case JsonQueryParserIN:
		return operation.IN
	}

	return nil
}

// versionCompare is a VersionOperation comparison whose rule version was
// parsed at compile time.
type versionCompare struct {
	// want reports whether left.Compare(right) satisfies the operator.
	want  func(cmp int) bool
	right semver.Version
}

// compileVersionCompare returns a comparison against the pre-parsed version
// right for the six comparison operators, or nil to keep VersionOperation
// (other operators, or a rule version semver rejects: its error is then
// produced on every Process exactly as before).
func compileVersionCompare(right string, opType int) *versionCompare {
	var want func(int) bool

	switch opType {
	case JsonQueryParserEQ:
		want = func(c int) bool { return c == 0 }
	case JsonQueryParserNE:
		want = func(c int) bool { return c != 0 }
	case JsonQueryParserGT:
		want = func(c int) bool { return c > 0 }
	case JsonQueryParserLT:
		want = func(c int) bool { return c < 0 }
	case JsonQueryParserGE:
		want = func(c int) bool { return c >= 0 }
	case JsonQueryParserLE:
		want = func(c int) bool { return c <= 0 }
	default:
		return nil
	}

	ver, err := semver.Make(right)
	if err != nil {
		return nil
	}

	return &versionCompare{right: ver, want: want}
}

// compare mirrors VersionOperation.get followed by the comparison; the rule
// operand is always the compiled version string, so only left is checked.
func (c *versionCompare) compare(left Operand) (bool, error) {
	leftVal, ok := left.(string)
	if !ok {
		return false, newInvalidOperandError(left, leftVal)
	}

	leftVer, ok := parsePlainVersion(leftVal)
	if !ok {
		var err error

		leftVer, err = semver.Make(leftVal)
		if err != nil {
			//nolint:wrapcheck // evaluation hot path: preserves semver error parity with VersionOperation.get
			return false, err
		}
	}

	return c.want(leftVer.Compare(c.right)), nil
}

// parsePlainVersion parses "MAJOR.MINOR.PATCH" with optional "+build"
// metadata without allocating. It only accepts strings that semver.Make
// accepts, and returns the same Major, Minor and Patch with no prerelease;
// build metadata is validated but dropped since Compare ignores it. Anything
// else (prerelease, errors) reports false so the caller uses semver.Make.
func parsePlainVersion(raw string) (semver.Version, bool) {
	var ver semver.Version

	core := raw
	// IndexByte rather than strings.Cut: this runs on every version evaluation and Cut adds a call layer.
	if i := strings.IndexByte(raw, '+'); i >= 0 { //nolint:modernize // see above
		if !validBuild(raw[i+1:]) {
			return ver, false
		}

		core = raw[:i]
	}

	var nums [3]uint64
	for i := range nums {
		end := len(core)
		if i < versionDots {
			end = strings.IndexByte(core, '.')
			if end < 0 {
				return ver, false
			}
		}

		n, ok := parseVersionNumber(core[:end])
		if !ok {
			return ver, false
		}

		nums[i] = n
		if i < versionDots {
			core = core[end+1:]
		}
	}

	ver.Major, ver.Minor, ver.Patch = nums[0], nums[1], nums[2]

	return ver, true
}

// parseVersionNumber accepts a non-empty run of digits without leading
// zeroes that fits in a uint64, like semver.Parse does for a version number.
func parseVersionNumber(raw string) (uint64, bool) {
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return 0, false
	}

	for i := range len(raw) {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}

	n, err := strconv.ParseUint(raw, 10, 64)

	return n, err == nil
}

// validBuild reports whether build is dot separated, non-empty identifiers of
// [0-9A-Za-z-], the build metadata semver.Parse accepts.
func validBuild(build string) bool {
	start := 0

	for i := 0; i <= len(build); i++ {
		if i == len(build) || build[i] == '.' {
			if i == start {
				return false
			}

			start = i + 1

			continue
		}

		c := build[i]

		valid := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-'
		if !valid {
			return false
		}
	}

	return true
}

// compileValue pre-parses the right-hand side of a comparison into target and
// returns the Operation the visitor would have selected for it.
func compileValue(target *compareNode, v IValueContext, src *sourceText) Operation {
	switch ctx := v.(type) {
	case *BooleanContext:
		// the BOOLEAN token only matches true or false, so this cannot fail
		target.rightOp = ctx.GetText() == "true"

		return &BoolOperation{}
	case *NullContext:
		return &NullOperation{}
	case *StringContext:
		target.rightOp = getString(ctx.GetText())

		return &StringOperation{}
	case *DoubleContext:
		// an unparsable double leaves the operand nil without an error
		val, err := strconv.ParseFloat(ctx.GetText(), 64)
		if err == nil {
			target.rightOp = val
		}

		return &FloatOperation{}
	case *VersionContext:
		target.rightOp = ctx.VERSION().GetText()

		return &VersionOperation{}
	case *LongContext:
		val, err := strconv.Atoi(ctx.GetText())
		if err != nil {
			target.valErr = err
		} else {
			target.rightOp = val
		}

		return &IntOperation{}
	case *ListOfIntsContext:
		target.compileInts(ctx)

		return &IntOperation{}
	case *ListOfDoublesContext:
		target.compileDoubles(ctx)

		return &FloatOperation{}
	case *ListOfStringsContext:
		target.compileStrings(ctx, src)

		return &StringOperation{}
	}

	return &NullOperation{}
}

// compileInts pre-parses an int list into n, with a hash index when it is long.
// A bad element stops the list and is kept as the list error.
func (n *compareNode) compileInts(ctx *ListOfIntsContext) {
	n.valKind = valueInts
	ints := []int{}

	for sub := ctx.ListInts().SubListOfInts(); ; {
		val, err := strconv.Atoi(sub.INT().GetText())
		if err != nil {
			n.listErr = err

			break
		}

		ints = append(ints, val)

		next := sub.SubListOfInts()
		if next == nil || next.IsEmpty() {
			break
		}

		sub = next
	}

	capped := slices.Clip(ints)

	n.listOp = capped
	if len(ints) >= inSetMinLen && len(ints) < math.MaxUint32 {
		n.intSet = newIntSet(capped)
	}
}

// compileDoubles pre-parses a float list into n. A bad element stops the list and
// is kept as the list error.
func (n *compareNode) compileDoubles(ctx *ListOfDoublesContext) {
	n.valKind = valueDoubles
	doubles := []float64{}

	for sub := ctx.ListDoubles().SubListOfDoubles(); ; {
		val, err := strconv.ParseFloat(sub.DOUBLE().GetText(), 64)
		if err != nil {
			n.listErr = err

			break
		}

		doubles = append(doubles, val)

		next := sub.SubListOfDoubles()
		if next == nil || next.IsEmpty() {
			break
		}

		sub = next
	}

	n.listOp = slices.Clip(doubles)
}

// compileStrings pre-parses a string list into n, packed with a hash index when it
// is long enough.
func (n *compareNode) compileStrings(ctx *ListOfStringsContext, src *sourceText) {
	n.valKind = valueStrings

	var tokens []antlr.Token
	for sub := ctx.ListStrings().SubListOfStrings(); ; {
		tokens = append(tokens, sub.STRING().GetSymbol())

		next := sub.SubListOfStrings()
		if next == nil || next.IsEmpty() {
			break
		}

		sub = next
	}

	if len(tokens) >= inSetMinLen {
		if set := newStrSet(tokens, src); set != nil {
			n.strSet = set

			return
		}
	}

	strs := make([]string, len(tokens))
	for i, t := range tokens {
		strs[i] = getString(t.GetText())
	}

	n.listOp = slices.Clip(strs)
}
