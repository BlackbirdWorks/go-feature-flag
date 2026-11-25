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

	tree, err := parseRule(rule)
	if err != nil {
		return nil, err
	}

	return &Evaluator{
		rule: rule,
		tree: tree,
	}, nil
}

//nolint:ireturn // returns the antlr.ParseTree that the generated parser constructs, which only exposes the interface
func parseRule(rule string) (antlr.ParseTree, error) {
	input := antlr.NewInputStream(normalizeRuleInput(rule))
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
		return nil, fmt.Errorf("%w %q: %s", errInvalidRule, rule, strings.Join(errListener.errs, "; "))
	}

	consumeTrailingWhitespace(tokens)

	if tokens.LA(1) != antlr.TokenEOF {
		return nil, fmt.Errorf("%w %q: unexpected input %q", errInvalidRule, rule, tokens.LT(1).GetText())
	}

	return tree, nil
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

	return assertType[bool](result), visitor.err
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

// assertType returns v as a T. A mismatch is a bug of the evaluator, not a bad rule: it panics, and the recover of
// the evaluation reports it as an error, as the unchecked type assertion it replaces did.
//
//nolint:ireturn // generic: the result is a type parameter
func assertType[T any](v any) T {
	res, ok := v.(T)
	if !ok {
		panic(fmt.Sprintf("interface conversion: %T is not %s", v, reflect.TypeFor[T]()))
	}

	return res
}
