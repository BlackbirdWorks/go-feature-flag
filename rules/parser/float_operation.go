package parser

import "slices"

// FloatOperation compares floating-point operands.
type FloatOperation struct {
	NullOperation
}

// EQ reports whether two floating-point operands are equal.
func (o *FloatOperation) EQ(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l == r, nil
}

// NE reports whether two floating-point operands are not equal.
func (o *FloatOperation) NE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l != r, nil
}

// GT reports whether the left operand is greater than the right operand.
func (o *FloatOperation) GT(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l > r, nil
}

// LT reports whether the left operand is less than the right operand.
func (o *FloatOperation) LT(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l < r, nil
}

// GE reports whether the left operand is greater than or equal to the right operand.
func (o *FloatOperation) GE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l >= r, nil
}

// LE reports whether the left operand is less than or equal to the right operand.
func (o *FloatOperation) LE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l <= r, nil
}

// IN reports whether the left operand is one of the values in the right operand.
func (o *FloatOperation) IN(left, right Operand) (bool, error) {
	leftVal, err := toFloat(left)
	if err != nil {
		return false, err
	}

	rightVal, ok := right.([]float64)
	if !ok {
		return false, newInvalidOperandError(right, rightVal)
	}

	if slices.Contains(rightVal, leftVal) {
		return true, nil
	}

	return false, nil
}

func (o *FloatOperation) get(left, right Operand) (float64, float64, error) {
	if left == nil {
		return 0, 0, ErrEvalOperandMissing
	}

	leftVal, err := toFloat(left)
	if err != nil {
		return 0, 0, err
	}

	rightVal, err := toFloat(right)

	return leftVal, rightVal, err
}
