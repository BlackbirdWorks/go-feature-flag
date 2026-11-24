package parser

type FloatOperation struct {
	NullOperation
}

func (o *FloatOperation) get(left Operand, right Operand) (float64, float64, error) {
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

func (o *FloatOperation) EQ(left Operand, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l == r, nil
}

func (o *FloatOperation) NE(left Operand, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l != r, nil
}

func (o *FloatOperation) GT(left Operand, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l > r, nil
}

func (o *FloatOperation) LT(left Operand, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l < r, nil
}

func (o *FloatOperation) GE(left Operand, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, err
	}

	return l >= r, nil
}

func (o *FloatOperation) LE(left Operand, right Operand) (bool, error) {
	l, r, err := o.get(left, right)
	if err != nil {
		return false, nil //nolint:nilerr // see issue #50: kept, behaviour change
	}

	return l <= r, nil
}

func (o *FloatOperation) IN(left Operand, right Operand) (bool, error) {
	leftVal, err := toFloat(left)
	if err != nil {
		return false, err
	}
	rightVal, ok := right.([]float64)
	if !ok {
		return false, newInvalidOperandError(right, rightVal)
	}
	// an explicit loop: slices.Contains is measurably slower on this evaluation path
	for _, num := range rightVal { //nolint:modernize // see above
		if num == leftVal {
			return true, nil
		}
	}

	return false, nil
}
