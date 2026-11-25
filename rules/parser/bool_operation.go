package parser

// BoolOperation compares boolean operands for equality and inequality.
type BoolOperation struct {
	NullOperation
}

// EQ reports whether two boolean operands are equal.
func (o *BoolOperation) EQ(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l == r, nil
}

// NE reports whether two boolean operands are not equal.
func (o *BoolOperation) NE(left, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l != r, nil
}

func (_ *BoolOperation) get(left, right Operand) (bool, bool, error) {
	if left == nil {
		return false, false, ErrEvalOperandMissing
	}

	leftVal, ok := left.(bool)
	if !ok {
		return false, false, newInvalidOperandError(left, leftVal)
	}

	rightVal, ok := right.(bool)
	if !ok {
		return false, false, newInvalidOperandError(right, rightVal)
	}

	return leftVal, rightVal, nil
}
