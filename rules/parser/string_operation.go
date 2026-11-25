package parser

import (
	"fmt"
	"slices"
	"strings"
)

// StringOperation compares string operands.
type StringOperation struct {
	NullOperation
}

// EQ reports whether two string operands are equal.
func (o *StringOperation) EQ(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l == r, nil
}

// NE reports whether two string operands are not equal.
func (o *StringOperation) NE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l != r, nil
}

// GT reports whether the left string operand sorts after the right one.
func (o *StringOperation) GT(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l > r, nil
}

// LT reports whether the left string operand sorts before the right one.
func (o *StringOperation) LT(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l < r, nil
}

// GE reports whether the left string operand sorts at or after the right one.
func (o *StringOperation) GE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l >= r, nil
}

// LE reports whether the left string operand sorts at or before the right one.
func (o *StringOperation) LE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l <= r, nil
}

// CO reports whether the left string operand contains the right one.
func (o *StringOperation) CO(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return strings.Contains(l, r), nil
}

// SW reports whether the left string operand starts with the right one.
func (o *StringOperation) SW(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return strings.HasPrefix(l, r), nil
}

// EW reports whether the left string operand ends with the right one.
func (o *StringOperation) EW(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return strings.HasSuffix(l, r), nil
}

// IN reports whether the left string operand is one of the values in the right operand.
func (o *StringOperation) IN(left, right Operand) (bool, error) {
	leftVal, err := o.getString(left)
	if err != nil {
		return false, err
	}

	rightVal, ok := right.([]string)
	if !ok {
		return ok, newInvalidOperandError(right, rightVal)
	}

	if slices.Contains(rightVal, leftVal) {
		return true, nil
	}

	return false, nil
}

func (*StringOperation) getString(operand Operand) (string, error) {
	switch opVal := operand.(type) {
	case string:
		return opVal, nil
	case fmt.Stringer:
		return opVal.String(), nil
	default:
		return "", newInvalidOperandError(operand, opVal)
	}
}

func (o *StringOperation) get(left, right Operand) (string, string, error) {
	if left == nil {
		return "", "", ErrEvalOperandMissing
	}

	leftVal, err := o.getString(left)
	if err != nil {
		return "", "", err
	}

	rightVal, err := o.getString(right)
	if err != nil {
		return "", "", err
	}

	return strings.ToLower(leftVal), strings.ToLower(rightVal), nil
}
