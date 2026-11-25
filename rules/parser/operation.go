package parser

import (
	"errors"
	"fmt"
	"reflect"
)

const (
	attrPath    = "attr_path"
	ruleOperand = "rule_operand"
)

func toFloat(operand Operand) (float64, error) {
	switch val := operand.(type) {
	case int:
		return float64(val), nil
	case float64:
		return val, nil
	}

	var exp float64

	return 0, newInvalidOperandError(operand, exp)
}

func toInt(operand Operand) (int, error) {
	switch val := operand.(type) {
	case int:
		return val, nil
	case float64:
		return int(val), nil
	case int32:
		return int(val), nil
	case int64:
		return int(val), nil
	}

	var exp int

	return 0, newInvalidOperandError(operand, exp)
}

// Operand is a value that an operation compares, such as a string, number or boolean.
type Operand any

// The error text is observable by callers (it is wrapped into the query validation error and logged),
// so it keeps its original capitalization.
var (
	ErrInvalidOperation   = errors.New("Invalid operation on the type") //nolint:staticcheck // ST1005: observable text
	ErrEvalOperandMissing = errors.New("Operand not present")
)

// Keys of the values attached to a NestedError by the evaluators.
const (
	errValOperation     = "operation"
	errValObjectOperand = "object_path_operand"
	errValRuleOperand   = ruleOperand
	errValAttrPath      = attrPath
)

// InvalidOperandError reports an operand whose type does not match the operation.
type InvalidOperandError struct {
	Val     any
	typeObj any
}

func newInvalidOperandError(val Operand, typeObj any) *InvalidOperandError {
	return &InvalidOperandError{
		Val:     val,
		typeObj: typeObj,
	}
}

func (e *InvalidOperandError) Error() string {
	return fmt.Sprintf("Operand %v is not the correct type. Expected: %s, Actual: %s",
		e.Val,
		reflect.TypeOf(e.typeObj).String(),
		reflect.TypeOf(e.Val).String(),
	)
}

// Operation compares two operands with the query operators.
type Operation interface {
	EQ(left Operand, right Operand) (bool, error)
	NE(left Operand, right Operand) (bool, error)
	GT(left Operand, right Operand) (bool, error)
	LT(left Operand, right Operand) (bool, error)
	GE(left Operand, right Operand) (bool, error)
	LE(left Operand, right Operand) (bool, error)
	CO(left Operand, right Operand) (bool, error)
	SW(left Operand, right Operand) (bool, error)
	EW(left Operand, right Operand) (bool, error)
	IN(left Operand, right Operand) (bool, error)
}
