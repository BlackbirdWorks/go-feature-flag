package parser

// NullOperation compares operands that may be null.
type NullOperation struct {
}

// EQ reports whether the left operand is nil.
func (o *NullOperation) EQ(left Operand, _ Operand) (bool, error) {
	return left == nil, nil
}

// NE reports whether the left operand is not nil.
func (o *NullOperation) NE(left Operand, _ Operand) (bool, error) {
	return left != nil, nil
}

// GT always returns ErrInvalidOperation because null operands cannot be ordered.
func (o *NullOperation) GT(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// LT always returns ErrInvalidOperation because null operands cannot be ordered.
func (o *NullOperation) LT(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// GE always returns ErrInvalidOperation because null operands cannot be ordered.
func (o *NullOperation) GE(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// LE always returns ErrInvalidOperation because null operands cannot be ordered.
func (o *NullOperation) LE(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// CO always returns ErrInvalidOperation because null operands cannot be searched.
func (o *NullOperation) CO(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// SW always returns ErrInvalidOperation because null operands cannot be searched.
func (o *NullOperation) SW(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// EW always returns ErrInvalidOperation because null operands cannot be searched.
func (o *NullOperation) EW(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

// IN always returns ErrInvalidOperation because null operands cannot be searched.
func (o *NullOperation) IN(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}
