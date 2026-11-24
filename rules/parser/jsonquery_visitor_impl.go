package parser

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/antlr4-go/antlr/v4"
)

var (
	errUnknownOperation = errors.New("Unknown operation")
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

type JsonQueryVisitorImpl struct {
	antlr.ParseTreeVisitor

	item map[string]any

	stack *objStack

	currentOperation Operation
	leftOp           Operand
	rightOp          Operand

	err      error
	debugErr error
}

func NewJsonQueryVisitorImpl(item map[string]any) *JsonQueryVisitorImpl {
	return &JsonQueryVisitorImpl{
		stack: &objStack{},
		item:  item,
	}
}

func (j *JsonQueryVisitorImpl) setDebugErr(err error) {
	j.debugErr = err
}

func (j *JsonQueryVisitorImpl) setErr(err error) {
	j.err = err
}

func (j *JsonQueryVisitorImpl) hasErr() bool {
	return j.err != nil
}

func (j *JsonQueryVisitorImpl) Visit(tree antlr.ParseTree) any {
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
		j.setErr(errInvalidRule2) //nolint:staticcheck // ST1005: text is observable, kept

		return false
	}
}

func (j *JsonQueryVisitorImpl) VisitParenExp(ctx *ParenExpContext) any {
	result := ctx.Query().Accept(j).(bool) //nolint:errcheck // panics on mismatch, as before
	if ctx.NOT() != nil {
		return !result
	}

	return result
}

func (j *JsonQueryVisitorImpl) VisitLogicalExp(ctx *LogicalExpContext) any {
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

func (j *JsonQueryVisitorImpl) VisitPresentExp(ctx *PresentExpContext) any {
	ctx.AttrPath().Accept(j)

	return j.leftOp != nil
}

func (j *JsonQueryVisitorImpl) VisitCompareExp(ctx *CompareExpContext) any {
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
		) //nolint:staticcheck // ST1005: text is observable
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
func (j *JsonQueryVisitorImpl) recordCompareError(ctx *CompareExpContext, err error) {
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

func (j *JsonQueryVisitorImpl) VisitAttrPath(ctx *AttrPathContext) any {
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

func (j *JsonQueryVisitorImpl) VisitSubAttr(ctx *SubAttrContext) any {
	return ctx.AttrPath().Accept(j)
}

func (j *JsonQueryVisitorImpl) VisitBoolean(ctx *BooleanContext) any {
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

func (j *JsonQueryVisitorImpl) VisitNull(ctx *NullContext) any {
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

func (j *JsonQueryVisitorImpl) VisitString(ctx *StringContext) any {
	j.currentOperation = &StringOperation{}
	j.rightOp = getString(ctx.GetText())

	return nil
}

func (j *JsonQueryVisitorImpl) VisitDouble(ctx *DoubleContext) any {
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

func (j *JsonQueryVisitorImpl) VisitVersion(ctx *VersionContext) any {
	j.currentOperation = &VersionOperation{}
	j.rightOp = ctx.VERSION().GetText()

	return nil
}

func (j *JsonQueryVisitorImpl) VisitLong(ctx *LongContext) any {
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

func (j *JsonQueryVisitorImpl) VisitListOfInts(ctx *ListOfIntsContext) any {
	j.currentOperation = &IntOperation{}

	return ctx.ListInts().Accept(j)
}

func (j *JsonQueryVisitorImpl) VisitListInts(ctx *ListIntsContext) any {
	return ctx.SubListOfInts().Accept(j)
}

func (j *JsonQueryVisitorImpl) VisitSubListOfInts(ctx *SubListOfIntsContext) any {
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

func (j *JsonQueryVisitorImpl) VisitListOfDoubles(ctx *ListOfDoublesContext) any {
	j.currentOperation = &FloatOperation{}

	return ctx.ListDoubles().Accept(j)
}

func (j *JsonQueryVisitorImpl) VisitListDoubles(ctx *ListDoublesContext) any {
	return ctx.SubListOfDoubles().Accept(j)
}

func (j *JsonQueryVisitorImpl) VisitSubListOfDoubles(ctx *SubListOfDoublesContext) any {
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

func (j *JsonQueryVisitorImpl) VisitListOfStrings(ctx *ListOfStringsContext) any {
	j.currentOperation = &StringOperation{}

	return ctx.ListStrings().Accept(j)
}

func (j *JsonQueryVisitorImpl) VisitListStrings(ctx *ListStringsContext) any {
	return ctx.SubListOfStrings().Accept(j)
}

func (j *JsonQueryVisitorImpl) VisitSubListOfStrings(ctx *SubListOfStringsContext) any {
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
