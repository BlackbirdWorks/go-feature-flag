package parser

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/antlr4-go/antlr/v4"
)

var (
	errUnknownOperation = errors.New("Unknown operation") //nolint:staticcheck // ST1005: the text is observable
)

type objStack struct {
	items []any
}

func (o *objStack) empty() bool {
	return len(o.items) == 0
}

func (o *objStack) peek() any {
	return o.items[len(o.items)-1]
}

func (o *objStack) pop() any {
	val := o.peek()
	o.items = o.items[:len(o.items)-1]

	return val
}

func (o *objStack) push(item any) {
	o.items = append(o.items, item)
}

func (o *objStack) clear() {
	o.items = nil
}

// JSONQueryVisitorImpl walks a parsed JSON query tree and evaluates it against an item.
type JSONQueryVisitorImpl struct {
	antlr.ParseTreeVisitor

	item map[string]any

	stack *objStack

	currentOperation Operation
	leftOp           Operand
	rightOp          Operand

	err      error
	debugErr error
}

// NewJSONQueryVisitorImpl returns a visitor that evaluates queries against item.
func NewJSONQueryVisitorImpl(item map[string]any) *JSONQueryVisitorImpl {
	return &JSONQueryVisitorImpl{
		stack: &objStack{},
		item:  item,
	}
}

// Visit dispatches a parse tree node to the matching visit method and returns false once an error is recorded.
func (j *JSONQueryVisitorImpl) Visit(tree antlr.ParseTree) any {
	if j.hasErr() {
		return false
	}

	switch val := tree.(type) {
	case *LogicalExpContext:
		return assertBool(val.Accept(j))
	case *CompareExpContext:
		return assertBool(val.Accept(j))
	case *ParenExpContext:
		return assertBool(val.Accept(j))
	case *PresentExpContext:
		return assertBool(val.Accept(j))
	default:
		j.setErr(errInvalidRule2)

		return false
	}
}

// VisitParenExp evaluates a parenthesized query and negates it when NOT is present.
func (j *JSONQueryVisitorImpl) VisitParenExp(ctx *ParenExpContext) any {
	result := assertBool(ctx.Query().Accept(j))
	if ctx.NOT() != nil {
		return !result
	}

	return result
}

// VisitLogicalExp evaluates an and/or expression of two queries.
func (j *JSONQueryVisitorImpl) VisitLogicalExp(ctx *LogicalExpContext) any {
	left := assertBool(ctx.Query(0).Accept(j))

	op := ctx.LOGICAL_OPERATOR().GetText()
	if op == "or" {
		if left {
			return left
		}

		return assertBool(ctx.Query(1).Accept(j))
	}
	// means it is and
	if !left {
		return left
	}

	return assertBool(ctx.Query(1).Accept(j))
}

// VisitPresentExp reports whether the attribute path resolves to a value.
func (j *JSONQueryVisitorImpl) VisitPresentExp(ctx *PresentExpContext) any {
	ctx.AttrPath().Accept(j)

	return j.leftOp != nil
}

// VisitCompareExp evaluates a comparison between an attribute path and a value.
func (j *JSONQueryVisitorImpl) VisitCompareExp(ctx *CompareExpContext) any {
	ctx.AttrPath().Accept(j)
	ctx.Value().Accept(j)

	if j.hasErr() {
		return false
	}

	apply := operationFor(j.currentOperation, ctx.op.GetTokenType())
	if apply == nil {
		err := fmt.Errorf(
			"%w %s",
			errUnknownOperation,
			ctx.op.GetText(),
		)
		j.setErr(err)

		return false
	}
	defer func() { j.rightOp = nil }()

	ret, err := apply(j.leftOp, j.rightOp)
	if err != nil {
		j.recordCompareError(ctx, err)

		return false
	}

	return ret
}

// operationFor returns the method of operation that implements the comparison of the token, nil if there is none.
func operationFor(operation Operation, tokenType int) func(Operand, Operand) (bool, error) {
	switch tokenType {
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
	default:
		return nil
	}
}

// VisitAttrPath resolves an attribute path against the item and records the left operand.
func (j *JSONQueryVisitorImpl) VisitAttrPath(ctx *AttrPathContext) any {
	var item any

	if ctx.SubAttr() == nil || ctx.SubAttr().IsEmpty() {
		if !j.stack.empty() {
			item = j.stack.pop()
		} else {
			item = j.item
		}

		if item == nil {
			return nil
		}

		m := assertMap(item)
		j.leftOp = m[ctx.ATTRNAME().GetText()]
		j.stack.clear()

		return nil
	}

	if !j.stack.empty() {
		item = j.stack.peek()
	} else {
		item = j.item
	}

	if item == nil {
		return nil
	}

	m := assertMap(item)
	j.stack.push(m[ctx.ATTRNAME().GetText()])

	return ctx.SubAttr().Accept(j)
}

// VisitSubAttr resolves a nested attribute path.
func (j *JSONQueryVisitorImpl) VisitSubAttr(ctx *SubAttrContext) any {
	return ctx.AttrPath().Accept(j)
}

// VisitBoolean records a boolean literal as the right operand.
func (j *JSONQueryVisitorImpl) VisitBoolean(ctx *BooleanContext) any {
	j.currentOperation = &BoolOperation{}

	val, err := strconv.ParseBool(ctx.GetText())
	if err != nil {
		j.rightOp = nil
		j.setErr(newNestedError(err, "Error converting boolean"))

		return nil
	}

	j.rightOp = val

	return nil
}

// VisitNull records a null literal as the right operand.
func (j *JSONQueryVisitorImpl) VisitNull(_ *NullContext) any {
	j.currentOperation = &NullOperation{}
	j.rightOp = nil

	return nil
}

// quoteChars is the number of characters of the quotes around a string literal.
const quoteChars = 2

func getString(s string) string {
	if len(s) > quoteChars {
		return s[1 : len(s)-1]
	}

	return ""
}

// VisitString records a string literal as the right operand.
func (j *JSONQueryVisitorImpl) VisitString(ctx *StringContext) any {
	j.currentOperation = &StringOperation{}
	j.rightOp = getString(ctx.GetText())

	return nil
}

// VisitDouble records a floating-point literal as the right operand.
func (j *JSONQueryVisitorImpl) VisitDouble(ctx *DoubleContext) any {
	j.currentOperation = &FloatOperation{}

	val, err := strconv.ParseFloat(ctx.GetText(), 64)
	if err != nil {
		// an unparsable double leaves the operand nil without an error, like the compiled path
		j.rightOp = nil

		return false
	}

	j.rightOp = val

	return nil
}

// VisitVersion records a version literal as the right operand.
func (j *JSONQueryVisitorImpl) VisitVersion(ctx *VersionContext) any {
	j.currentOperation = &VersionOperation{}
	j.rightOp = ctx.VERSION().GetText()

	return nil
}

// VisitLong records an integer literal as the right operand.
func (j *JSONQueryVisitorImpl) VisitLong(ctx *LongContext) any {
	j.currentOperation = &IntOperation{}

	val, err := strconv.Atoi(ctx.GetText())
	if err != nil {
		j.rightOp = nil
		j.setErr(err)

		return nil
	}

	j.rightOp = val

	return nil
}

// VisitListOfInts records a list of integers as the right operand.
func (j *JSONQueryVisitorImpl) VisitListOfInts(ctx *ListOfIntsContext) any {
	j.currentOperation = &IntOperation{}

	return ctx.ListInts().Accept(j)
}

// VisitListInts visits the integer list of a query.
func (j *JSONQueryVisitorImpl) VisitListInts(ctx *ListIntsContext) any {
	return ctx.SubListOfInts().Accept(j)
}

// VisitSubListOfInts appends one integer to the right operand list.
func (j *JSONQueryVisitorImpl) VisitSubListOfInts(ctx *SubListOfIntsContext) any {
	if j.rightOp == nil {
		j.rightOp = make([]int, 0)
	}

	list := assertInts(j.rightOp)

	val, err := strconv.Atoi(ctx.INT().GetText())
	if err != nil {
		j.setErr(err)

		return nil
	}

	list = append(list, val)
	j.rightOp = list

	if ctx.SubListOfInts() == nil || ctx.SubListOfInts().IsEmpty() {
		return nil
	}

	return ctx.SubListOfInts().Accept(j)
}

// VisitListOfDoubles records a list of floating-point numbers as the right operand.
func (j *JSONQueryVisitorImpl) VisitListOfDoubles(ctx *ListOfDoublesContext) any {
	j.currentOperation = &FloatOperation{}

	return ctx.ListDoubles().Accept(j)
}

// VisitListDoubles visits the floating-point list of a query.
func (j *JSONQueryVisitorImpl) VisitListDoubles(ctx *ListDoublesContext) any {
	return ctx.SubListOfDoubles().Accept(j)
}

// VisitSubListOfDoubles appends one floating-point number to the right operand list.
func (j *JSONQueryVisitorImpl) VisitSubListOfDoubles(ctx *SubListOfDoublesContext) any {
	if j.rightOp == nil {
		j.rightOp = make([]float64, 0)
	}

	list := assertFloats(j.rightOp)

	val, err := strconv.ParseFloat(ctx.DOUBLE().GetText(), 64)
	if err != nil {
		j.setErr(err)

		return nil
	}

	list = append(list, val)
	j.rightOp = list

	if ctx.SubListOfDoubles() == nil || ctx.SubListOfDoubles().IsEmpty() {
		return nil
	}

	return ctx.SubListOfDoubles().Accept(j)
}

// VisitListOfStrings records a list of strings as the right operand.
func (j *JSONQueryVisitorImpl) VisitListOfStrings(ctx *ListOfStringsContext) any {
	j.currentOperation = &StringOperation{}

	return ctx.ListStrings().Accept(j)
}

// VisitListStrings visits the string list of a query.
func (j *JSONQueryVisitorImpl) VisitListStrings(ctx *ListStringsContext) any {
	return ctx.SubListOfStrings().Accept(j)
}

// VisitSubListOfStrings appends one string to the right operand list.
func (j *JSONQueryVisitorImpl) VisitSubListOfStrings(ctx *SubListOfStringsContext) any {
	if j.rightOp == nil {
		j.rightOp = make([]string, 0)
	}

	val := getString(ctx.STRING().GetText())
	list := assertStrings(j.rightOp)
	list = append(list, val)
	j.rightOp = list

	if ctx.SubListOfStrings() == nil || ctx.SubListOfStrings().IsEmpty() {
		return nil
	}

	return ctx.SubListOfStrings().Accept(j)
}

func (j *JSONQueryVisitorImpl) setDebugErr(err error) {
	j.debugErr = err
}

func (j *JSONQueryVisitorImpl) setErr(err error) {
	j.err = err
}

func (j *JSONQueryVisitorImpl) hasErr() bool {
	return j.err != nil
}

// recordCompareError stores the error of a comparison: an invalid operation is the error of the rule, the
// other errors are only kept for debugging.
func (j *JSONQueryVisitorImpl) recordCompareError(ctx *CompareExpContext, err error) {
	switch {
	case errors.Is(err, ErrInvalidOperation):
		// in case of invalid operation lets rather
		// be conservative and return false because the rule doesn't even make
		// sense. It can be argued that it would be false positive if we were
		// to return true
		j.setErr(err)
		j.setDebugErr(
			newNestedError(err, "Not a valid operation for datatypes").Set(ErrVals{
				errValOperation:     ctx.op.GetTokenType(),
				errValObjectOperand: j.leftOp,
				errValRuleOperand:   j.rightOp,
			}),
		)
	case errors.Is(err, ErrEvalOperandMissing):
		j.setDebugErr(
			newNestedError(err, "Eval operand missing in input object").Set(ErrVals{
				errValAttrPath: ctx.AttrPath().GetText(),
			}),
		)
	default:
		msg := "unknown error"
		if _, ok := errors.AsType[*InvalidOperandError](err); ok {
			msg = "operands are not the right value type"
		}

		j.setDebugErr(
			newNestedError(err, msg).Set(ErrVals{
				errValAttrPath:      ctx.AttrPath().GetText(),
				errValObjectOperand: j.leftOp,
				errValRuleOperand:   j.rightOp,
			}),
		)
	}
}

// compile-time check of the interface implemented by the type.
var _ JsonQueryVisitor = (*JSONQueryVisitorImpl)(nil)
