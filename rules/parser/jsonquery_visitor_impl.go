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

func NewJSONQueryVisitorImpl(item map[string]any) *JSONQueryVisitorImpl {
	return &JSONQueryVisitorImpl{
		stack: &objStack{},
		item:  item,
	}
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

func (j *JSONQueryVisitorImpl) Visit(tree antlr.ParseTree) any {
	if j.hasErr() {
		return false
	}
	switch val := tree.(type) {
	case *LogicalExpContext:
		return val.Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	case *CompareExpContext:
		return val.Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	case *ParenExpContext:
		return val.Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	case *PresentExpContext:
		return val.Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	default:
		j.setErr(errInvalidRule2)

		return false
	}
}

func (j *JSONQueryVisitorImpl) VisitParenExp(ctx *ParenExpContext) any {
	result := ctx.Query().Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	if ctx.NOT() != nil {
		return !result
	}

	return result
}

func (j *JSONQueryVisitorImpl) VisitLogicalExp(ctx *LogicalExpContext) any {
	left := ctx.Query(0).Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	op := ctx.LOGICAL_OPERATOR().GetText()
	if op == "or" {
		if left {
			return left
		}

		return ctx.Query(1).Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	}
	// means it is and
	if !left {
		return left
	}

	return ctx.Query(1).Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
}

func (j *JSONQueryVisitorImpl) VisitPresentExp(ctx *PresentExpContext) any {
	ctx.AttrPath().Accept(j)

	return j.leftOp != nil
}

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

// operationFor returns the method of op that implements the comparison of the token, nil if there is none.
func operationFor(op Operation, tokenType int) func(Operand, Operand) (bool, error) {
	switch tokenType {
	case JsonQueryParserEQ:
		return op.EQ
	case JsonQueryParserNE:
		return op.NE
	case JsonQueryParserGT:
		return op.GT
	case JsonQueryParserLT:
		return op.LT
	case JsonQueryParserLE:
		return op.LE
	case JsonQueryParserGE:
		return op.GE
	case JsonQueryParserCO:
		return op.CO
	case JsonQueryParserSW:
		return op.SW
	case JsonQueryParserEW:
		return op.EW
	case JsonQueryParserIN:
		return op.IN
	default:
		return nil
	}
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
		m := item.(map[string]any) //nolint:errcheck // panics on mismatch, as before
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
	m := item.(map[string]any) //nolint:errcheck // panics on mismatch, as before
	j.stack.push(m[ctx.ATTRNAME().GetText()])

	return ctx.SubAttr().Accept(j)
}

func (j *JSONQueryVisitorImpl) VisitSubAttr(ctx *SubAttrContext) any {
	return ctx.AttrPath().Accept(j)
}

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

func (j *JSONQueryVisitorImpl) VisitString(ctx *StringContext) any {
	j.currentOperation = &StringOperation{}
	j.rightOp = getString(ctx.GetText())

	return nil
}

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

func (j *JSONQueryVisitorImpl) VisitVersion(ctx *VersionContext) any {
	j.currentOperation = &VersionOperation{}
	j.rightOp = ctx.VERSION().GetText()

	return nil
}

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

func (j *JSONQueryVisitorImpl) VisitListOfInts(ctx *ListOfIntsContext) any {
	j.currentOperation = &IntOperation{}

	return ctx.ListInts().Accept(j)
}

func (j *JSONQueryVisitorImpl) VisitListInts(ctx *ListIntsContext) any {
	return ctx.SubListOfInts().Accept(j)
}

func (j *JSONQueryVisitorImpl) VisitSubListOfInts(ctx *SubListOfIntsContext) any {
	if j.rightOp == nil {
		j.rightOp = make([]int, 0)
	}
	list := j.rightOp.([]int) //nolint:errcheck // panics on mismatch, as before
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

func (j *JSONQueryVisitorImpl) VisitListOfDoubles(ctx *ListOfDoublesContext) any {
	j.currentOperation = &FloatOperation{}

	return ctx.ListDoubles().Accept(j)
}

func (j *JSONQueryVisitorImpl) VisitListDoubles(ctx *ListDoublesContext) any {
	return ctx.SubListOfDoubles().Accept(j)
}

func (j *JSONQueryVisitorImpl) VisitSubListOfDoubles(ctx *SubListOfDoublesContext) any {
	if j.rightOp == nil {
		j.rightOp = make([]float64, 0)
	}
	list := j.rightOp.([]float64) //nolint:errcheck // panics on mismatch, as before
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

func (j *JSONQueryVisitorImpl) VisitListOfStrings(ctx *ListOfStringsContext) any {
	j.currentOperation = &StringOperation{}

	return ctx.ListStrings().Accept(j)
}

func (j *JSONQueryVisitorImpl) VisitListStrings(ctx *ListStringsContext) any {
	return ctx.SubListOfStrings().Accept(j)
}

func (j *JSONQueryVisitorImpl) VisitSubListOfStrings(ctx *SubListOfStringsContext) any {
	if j.rightOp == nil {
		j.rightOp = make([]string, 0)
	}
	val := getString(ctx.STRING().GetText())
	list := j.rightOp.([]string) //nolint:errcheck // panics on mismatch, as before
	list = append(list, val)
	j.rightOp = list
	if ctx.SubListOfStrings() == nil || ctx.SubListOfStrings().IsEmpty() {
		return nil
	}

	return ctx.SubListOfStrings().Accept(j)
}
