package parser

import (
	"fmt"

	"github.com/blang/semver/v4"
)

const (
	parsingTheVersionQ = "parsing the version %q: %w"
)

// VersionOperation compares semantic version operands.
type VersionOperation struct {
	NullOperation
}

// EQ reports whether two versions are equal.
func (v *VersionOperation) EQ(left Operand, right Operand) (bool, error) {
	leftVer, rightVer, err := v.get(left, right)
	if err != nil {
		return false, err
	}

	return leftVer.EQ(rightVer), nil
}

// NE reports whether two versions are not equal.
func (v *VersionOperation) NE(left Operand, right Operand) (bool, error) {
	leftVer, rightVer, err := v.get(left, right)
	if err != nil {
		return false, err
	}

	return leftVer.NE(rightVer), nil
}

// LT reports whether the left version is lower than the right one.
func (v *VersionOperation) LT(left Operand, right Operand) (bool, error) {
	leftVer, rightVer, err := v.get(left, right)
	if err != nil {
		return false, err
	}

	return leftVer.LT(rightVer), nil
}

// GT reports whether the left version is higher than the right one.
func (v *VersionOperation) GT(left Operand, right Operand) (bool, error) {
	leftVer, rightVer, err := v.get(left, right)
	if err != nil {
		return false, err
	}

	return leftVer.GT(rightVer), nil
}

// LE reports whether the left version is lower than or equal to the right one.
func (v *VersionOperation) LE(left Operand, right Operand) (bool, error) {
	leftVer, rightVer, err := v.get(left, right)
	if err != nil {
		return false, err
	}

	return leftVer.LE(rightVer), nil
}

// GE reports whether the left version is higher than or equal to the right one.
func (v *VersionOperation) GE(left Operand, right Operand) (bool, error) {
	leftVer, rightVer, err := v.get(left, right)
	if err != nil {
		return false, err
	}

	return leftVer.GE(rightVer), nil
}

func (v *VersionOperation) get(left Operand, right Operand) (semver.Version, semver.Version, error) {
	var leftVer, rightVer semver.Version

	leftVal, ok := left.(string)
	if !ok {
		return leftVer, rightVer, newInvalidOperandError(left, leftVal)
	}

	rightVal, ok := right.(string)
	if !ok {
		return leftVer, rightVer, newInvalidOperandError(right, rightVal)
	}

	var err error

	leftVer, err = semver.Make(leftVal)
	if err != nil {
		return leftVer, rightVer, fmt.Errorf(parsingTheVersionQ, leftVal, err)
	}

	rightVer, err = semver.Make(rightVal)
	if err != nil {
		return leftVer, rightVer, fmt.Errorf(parsingTheVersionQ, rightVal, err)
	}

	return leftVer, rightVer, nil
}
