package parser

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/antlr4-go/antlr/v4"
)

var (
	errInvalidRule  = errors.New("invalid rule")
	errInvalidRule2 = errors.New("Invalid rule") //nolint:staticcheck // ST1005: the text is observable
)

// Evaluator evaluates a single parsed rule against attribute maps.
//
// It is safe for concurrent Process calls. It must not be copied after first
// use: it holds a [sync.Once] and an [atomic.Pointer], and go vet's copylocks
// check flags copies. Each call clears LastDebugErr on entry; once calls
// quiesce it holds the error of the last call to finish (nil if that call was
// clean or panicked). Reset clears it.
type Evaluator struct {
	// tree is kept until the first Process compiles it into root, after
	// which it is dropped. If compiling fails, tree stays and Process falls
	// back to walking it with the visitor.
	tree antlr.ParseTree
	// lastDebugErr is atomic so concurrent Process calls do not race on it;
	// with concurrent callers it holds the error of whichever call finished last.
	lastDebugErr  atomic.Pointer[error]
	root          *node
	testHookPanic func()
	rule          string
	terms         []SegmentTerm
	once          sync.Once
}

// NewEvaluator parses and compiles a JSON query rule into an Evaluator.
func NewEvaluator(rule string) (ret *Evaluator, retErr error) { //nolint:nonamedreturns // recover sets the result
	// antlr lib has panics for exceptions so we have to put a recover here
	// in the unlikely case there is an exception
	defer func() {
		info := recover()
		if info != nil {
			retErr = recoveredPanicError{info: info}
		}
	}()

	text, hasSegments, err := scanSegments(rule)
	if err != nil {
		return nil, err
	}

	parsed, err := parseRuleText(rule, text)
	if err != nil {
		return nil, err
	}

	evaluator := &Evaluator{
		rule: rule,
		tree: parsed.tree,
	}
	if hasSegments {
		root, terms, compileErr := compileSegmentQuery(parsed.tree)
		if compileErr != nil {
			return nil, compileErr
		}

		evaluator.root, evaluator.terms, evaluator.tree = root, terms, nil
		evaluator.once.Do(func() {}) // compiled
	}

	return evaluator, nil
}

// HasSegments reports whether the rule has segment() terms. Such a rule is evaluated by ProcessSegments.
func (e *Evaluator) HasSegments() bool {
	return e.terms != nil
}

// SegmentTerms returns the segment terms of the rule, in the order of the states ProcessSegments reads. The
// result is shared: it must not be modified. It is nil when the rule has no segment term.
func (e *Evaluator) SegmentTerms() []SegmentTerm {
	return e.terms
}

// ProcessSegmentsDemand is ProcessSegments that also returns the terms that the result depends on: bit i is set
// when the state of SegmentTerms()[i] is SegmentUnknown and a result that is unknown could change if it were
// answered. When the match is not MatchUnknown the demand is 0: nothing needs to be looked up.
//
//nolint:nonamedreturns // recover sets the result
func (e *Evaluator) ProcessSegmentsDemand(
	items map[string]any,
	states []SegmentState,
) (ret Match, demand uint32, retErr error) {
	e.setDebugErr(nil)

	defer func() {
		info := recover()
		if info != nil {
			retErr = recoveredPanicError{info: info}
			ret, demand = MatchUnknown, 0

			e.setDebugErr(nil)
		}
	}()

	if len(states) != len(e.terms) {
		return MatchUnknown, 0, fmt.Errorf("%w: got %d, want %d", ErrSegmentStates, len(states), len(e.terms))
	}

	if e.root == nil {
		return MatchUnknown, 0, errInvalidRule2
	}

	st := evalState{item: items}
	result, wanted := e.root.evalDemand(&st, states)
	e.setDebugErr(st.debugErr)

	if st.err != nil {
		return MatchUnknown, 0, st.err
	}

	return result, wanted, nil
}

// ProcessSegments evaluates a rule that has segment() terms: states[i] is the membership for SegmentTerms()[i].
// The result is MatchUnknown when it depends on a term whose state is SegmentUnknown. Like Process, it is safe for
// concurrent use and it never modifies items or states.
func (e *Evaluator) ProcessSegments(items map[string]any, states []SegmentState) (Match, error) {
	match, _, err := e.ProcessSegmentsDemand(items, states)

	return match, err
}

type parsedRule struct {
	tree antlr.ParseTree
}

func parseRule(rule string) (parsedRule, error) {
	return parseRuleText(rule, rule)
}

// parseRuleText parses text, the rule once its segment() calls are replaced, and names rule in the errors.
func parseRuleText(rule, text string) (parsedRule, error) {
	input := antlr.NewInputStream(normalizeRuleInput(text))
	lex := NewJsonQueryLexer(input)
	errListener := &collectingErrorListener{DefaultErrorListener: antlr.NewDefaultErrorListener()}

	lex.RemoveErrorListeners()
	lex.AddErrorListener(errListener)
	tokens := antlr.NewCommonTokenStream(lex, antlr.TokenDefaultChannel)
	p := NewJsonQueryParser(tokens)
	p.RemoveErrorListeners()
	p.AddErrorListener(errListener)
	tree := p.Query()

	if len(errListener.errs) > 0 {
		return parsedRule{}, fmt.Errorf("%w %q: %s", errInvalidRule, rule, strings.Join(errListener.errs, "; "))
	}

	consumeTrailingWhitespace(tokens)

	if tokens.LA(1) != antlr.TokenEOF {
		return parsedRule{}, fmt.Errorf("%w %q: unexpected input %q", errInvalidRule, rule, tokens.LT(1).GetText())
	}

	return parsedRule{tree: tree}, nil
}

// Reset clears the last debug error recorded by the evaluator.
func (e *Evaluator) Reset() {
	e.lastDebugErr.Store(nil)
}

// LastDebugErr returns the last debug error recorded by the evaluator, or nil if there is none.
func (e *Evaluator) LastDebugErr() error {
	if p := e.lastDebugErr.Load(); p != nil {
		return *p
	}

	return nil
}

// Process evaluates items against the rule. The rule is compiled on first use.
//
//nolint:nonamedreturns // recover sets the result
func (e *Evaluator) Process(items map[string]any) (ret bool, retErr error) {
	e.setDebugErr(nil)
	// antlr lib has panics for exceptions so we have to put a recover here
	// in the unlikely case there is an exception
	defer func() {
		info := recover()
		if info != nil {
			retErr = recoveredPanicError{info: info}
			ret = false
			// a call that panics has no debug error, even if an overlapping
			// call stored its own after this one started
			e.setDebugErr(nil)
		}
	}()

	if e.terms != nil {
		return false, ErrSegmentsNotResolved
	}

	e.once.Do(e.compile)

	var (
		result bool
		err    error
	)

	switch {
	case e.root != nil:
		st := evalState{item: items}
		result = e.root.eval(&st)
		err = st.err
		e.setDebugErr(st.debugErr)
	case e.tree != nil:
		result, err = e.visit(items)
	default:
		err = errInvalidRule2
	}

	if e.testHookPanic != nil {
		defer e.testHookPanic()
	}

	if err != nil {
		return false, err
	}

	return result, nil
}

func (e *Evaluator) setDebugErr(err error) {
	if err == nil {
		// load first so concurrent clean calls on a shared evaluator only
		// read the shared cache line instead of all writing to it
		if e.lastDebugErr.Load() != nil {
			e.lastDebugErr.Store(nil)
		}

		return
	}

	box := err
	e.lastDebugErr.Store(&box)
}

// compile turns the parse tree into the node tree used by Process. It runs
// once; a panic leaves the tree in place so Process uses the visitor instead.
func (e *Evaluator) compile() {
	defer func() {
		// Deliberately silent: compiling is only an optimization. A panic
		// here (e.g. a tree shape compileQuery does not expect) must not
		// change what Process returns, so drop any partial result and keep
		// the tree; Process then walks it with the visitor, which reports
		// whatever error or result the uncompiled evaluator always did.
		if info := recover(); info != nil {
			e.root = nil
		}
	}()

	if e.tree == nil {
		return
	}

	e.root = compileQuery(e.tree)
	e.tree = nil
}

func (e *Evaluator) visit(items map[string]any) (bool, error) {
	visitor := NewJSONQueryVisitorImpl(items)
	result := visitor.Visit(e.tree)
	e.setDebugErr(visitor.debugErr)

	if result == nil || visitor.err != nil {
		return false, visitor.err
	}

	return assertBool(result), visitor.err
}

// Evaluate reports whether the rule matches the items, and false if the rule does not compile.
func Evaluate(rule string, items map[string]any) bool {
	ev, err := NewEvaluator(rule)
	if err != nil {
		return false
	}

	result, _ := ev.Process(items)

	return result
}

// recoveredPanicError is the error of a panic that the antlr runtime raised, in place of an error.
type recoveredPanicError struct{ info any }

func (e recoveredPanicError) Error() string { return fmt.Sprintf("%q", e.info) }

// The assert functions return v as the type they name. A mismatch is a bug of the evaluator, not a bad rule: they
// panic, and the recover of the evaluation reports it as an error, as the unchecked type assertion they replace did.

func assertBool(v any) bool {
	res, ok := v.(bool)
	if !ok {
		panicConversion(v, "bool")
	}

	return res
}

func assertString(v any) string {
	res, ok := v.(string)
	if !ok {
		panicConversion(v, "string")
	}

	return res
}

func assertMap(v any) map[string]any {
	res, ok := v.(map[string]any)
	if !ok {
		panicConversion(v, "map[string]any")
	}

	return res
}

func assertInts(v any) []int {
	return assertSlice[int](v)
}

func assertFloats(v any) []float64 {
	return assertSlice[float64](v)
}

func assertStrings(v any) []string {
	return assertSlice[string](v)
}

func assertSlice[E any](v any) []E {
	res, ok := v.([]E)
	if !ok {
		panicConversion(v, reflect.TypeFor[[]E]().String())
	}

	return res
}

func panicConversion(v any, want string) {
	panic(fmt.Sprintf("interface conversion: %T is not %s", v, want))
}
