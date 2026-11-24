package parser

type NullOperation struct {
}

func (o *NullOperation) EQ(left Operand, _ Operand) (bool, error) {
	return left == nil, nil
}

func (o *NullOperation) NE(left Operand, _ Operand) (bool, error) {
	return left != nil, nil
}

func (o *NullOperation) GT(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

func (o *NullOperation) LT(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

func (o *NullOperation) GE(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

func (o *NullOperation) LE(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

func (o *NullOperation) CO(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

func (o *NullOperation) SW(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

func (o *NullOperation) EW(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}

func (o *NullOperation) IN(_ Operand, _ Operand) (bool, error) {
	return false, ErrInvalidOperation
}
