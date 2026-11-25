package parser

// NullOperation compares operands that may be null.
type NullOperation struct {
}

// EQ reports whether the left operand is nil.
func (*NullOperation) EQ(left, _ Operand) (bool, error) {
	return left == nil, nil
}

// NE reports whether the left operand is not nil.
func (*NullOperation) NE(left, _ Operand) (bool, error) {
	return left != nil, nil
}

// GT always returns ErrInvalidOperation because null operands cannot be ordered.
func (*NullOperation) GT(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// LT always returns ErrInvalidOperation because null operands cannot be ordered.
func (*NullOperation) LT(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// GE always returns ErrInvalidOperation because null operands cannot be ordered.
func (*NullOperation) GE(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// LE always returns ErrInvalidOperation because null operands cannot be ordered.
func (*NullOperation) LE(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// CO always returns ErrInvalidOperation because null operands cannot be searched.
func (*NullOperation) CO(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// SW always returns ErrInvalidOperation because null operands cannot be searched.
func (*NullOperation) SW(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// EW always returns ErrInvalidOperation because null operands cannot be searched.
func (*NullOperation) EW(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// IN always returns ErrInvalidOperation because null operands cannot be searched.
func (*NullOperation) IN(_, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}
