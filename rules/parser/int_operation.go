package parser

import "slices"

// IntOperation compares integer operands.
type IntOperation struct {
	NullOperation
}

// EQ reports whether two integer operands are equal.
func (o *IntOperation) EQ(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l == r, nil
}

// NE reports whether two integer operands are not equal.
func (o *IntOperation) NE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l != r, nil
}

// GT reports whether the left operand is greater than the right operand.
func (o *IntOperation) GT(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l > r, nil
}

// LT reports whether the left operand is less than the right operand.
func (o *IntOperation) LT(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l < r, nil
}

// GE reports whether the left operand is greater than or equal to the right operand.
func (o *IntOperation) GE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l >= r, nil
}

// LE reports whether the left operand is less than or equal to the right operand.
func (o *IntOperation) LE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l <= r, nil
}

// IN reports whether the left operand is one of the values in the right operand.
func (_ *IntOperation) IN(left, right Operand) (bool, error) {
	leftVal, ok := left.(int)
	if !ok {
		return false, newInvalidOperandError(left, leftVal)
	}

	rightVal, ok := right.([]int)
	if !ok {
		return false, newInvalidOperandError(right, rightVal)
	}

	if slices.Contains(rightVal, leftVal) {
		return true, nil
	}

	return false, nil
}

func (_ *IntOperation) get(left, right Operand) (int, int, error) {
	if left == nil {
		return 0, 0, ErrEvalOperandMissing
	}

	leftVal, err := toInt(left)
	if err != nil {
		return 0, 0, err
	}

	rightVal, err := toInt(right)
	if err != nil {
		return 0, 0, err
	}

	return leftVal, rightVal, nil
}
