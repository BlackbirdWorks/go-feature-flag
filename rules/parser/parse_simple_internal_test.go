package parser

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	abc                = "abc"
	bbc                = "bbc"
	cde                = "cde"
	versionOneZeroZero = "1.0.0"
	versionOneOneZero  = "1.1.0"
	versionTwoZeroZero = "2.0.0"
	s2                 = "(%s)"
	x1                 = `x != 1`
	xEQ10              = `x EQ 1.0.0`
	xEq1               = `x eq 1`
	xEq10              = `x eq 1.0`
	xEqNull            = `x eq null`
	xGE11              = `x GE 1.1.0`
	xGT11              = `x GT 1.1.0`
	xGe1               = `x ge 1`
	xGeCde             = `x ge "cde"`
	xLE11              = `x LE 1.1.0`
	xLT11              = `x LT 1.1.0`
	xLe1               = `x le 1`
	xLe11              = `x le 1.1`
	xNE10              = `x NE 1.0.0`
)

type testCase struct {
	input    map[string]any
	rule     string
	result   bool
	hasError bool
}

type obj map[string]any

func eval(rule string, input obj) (bool, error) {
	ev, err := NewEvaluator(rule)
	if err != nil {
		return false, err
	}

	return ev.Process(input)
}

func TestInvalidRule(t *testing.T) {
	t.Parallel()

	invalidRules := []string{
		`x eq 1 and y ~ 1`,
		`y ~ 1`,
		`y ~ 1 and x eq true`,
	}

	for _, rule := range invalidRules {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()

			result, err := eval(rule, obj{"x": 1})
			require.Error(t, err, rule)
			assert.False(t, result)
		})
	}
}

func runVersionTests(t *testing.T, tests []testCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			result, err := eval(tt.rule, tt.input)
			if tt.hasError {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.result, result, "rule :%s, input :%v", tt.rule, tt.input)
			assert.False(t, Evaluate(tt.rule, obj{"x": 4.5}), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestVersions(t *testing.T) {
	t.Parallel()

	runVersionTests(t, []testCase{
		{
			rule: xEQ10,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xEQ10,
			input: obj{
				"x": 2,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xEQ10,
			input: obj{
				"y": versionOneZeroZero,
			},
			result:   false,
			hasError: false,
		},
		{
			rule:     xEQ10,
			input:    obj{},
			result:   false,
			hasError: false,
		},
		{
			rule: xEQ10,
			input: obj{
				"x": "1.1.1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xNE10,
			input: obj{
				"x": "1.0.0.",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xNE10,
			input: obj{
				"x": 2.3,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xNE10,
			input: obj{
				"x": versionOneOneZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLT11,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLT11,
			input: obj{
				"x": versionTwoZeroZero,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xLT11,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xLE11,
			input: obj{
				"x": versionOneOneZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLE11,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLE11,
			input: obj{
				"x": versionTwoZeroZero,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xLE11,
			input: obj{
				"x": 2,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xGT11,
			input: obj{
				"x": versionTwoZeroZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xGT11,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xGT11,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xGE11,
			input: obj{
				"x": versionTwoZeroZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xGE11,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xGE11,
			input: obj{
				"x": versionOneOneZero,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xGE11,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x CO 1.1.0`,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   true,
			hasError: true,
		},
		{
			rule: `x SW 1.1.0`,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   true,
			hasError: true,
		},
		{
			rule: `x EW 1.1.0`,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   true,
			hasError: true,
		},
		{
			rule: `x SW 1.1.0`,
			input: obj{
				"x": versionOneZeroZero,
			},
			result:   true,
			hasError: true,
		},
	})
}

func TestVersions_PrereleaseAndBuild(t *testing.T) {
	t.Parallel()

	// ensure prerelease comparisons work per semver spec
	runVersionTests(t, []testCase{
		{
			rule: xLT11,
			input: obj{
				"x": "1.1.0-alpha",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x GT 1.1.0-alpha`,
			input: obj{
				"x": "1.1.0-beta",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x EQ 1.1.0-alpha.1`,
			input: obj{
				"x": "1.1.0-alpha.1",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x EQ 1.0.0-0`,
			input: obj{
				"x": "1.0.0-0",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-alpha`,
			input: obj{
				"x": "1.0.0-0",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x GT 1.0.0-alpha.1`,
			input: obj{
				"x": "1.0.0-alpha.beta",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-beta.2`,
			input: obj{
				"x": "1.0.0-alpha.beta",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-beta.11`,
			input: obj{
				"x": "1.0.0-beta.2",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-rc.1`,
			input: obj{
				"x": "1.0.0-beta.11",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0`,
			input: obj{
				"x": "1.0.0-rc.1",
			},
			result:   true,
			hasError: false,
		},
		// build metadata is ignored in comparisons per semver spec
		{
			rule: xEQ10,
			input: obj{
				"x": "1.0.0+20130313144700",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x EQ 1.0.0-beta`,
			input: obj{
				"x": "1.0.0-beta+exp.sha.5114f85",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x EQ 1.1.0+build.1`,
			input: obj{
				"x": "1.1.0+build.2",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x EQ 1.1.0-alpha+build.1`,
			input: obj{
				"x": "1.1.0-alpha+build.2",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-alpha.10`,
			input: obj{
				"x": "1.0.0-alpha.2",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-alpha`,
			input: obj{
				"x": "1.0.0-1",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-alpha.beta`,
			input: obj{
				"x": "1.0.0-alpha.1",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x LT 1.0.0-alpha.1`,
			input: obj{
				"x": "1.0.0-alpha",
			},
			result:   true,
			hasError: false,
		},
	})
}

func TestListOfStrings(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: `x IN ["abc", "cde", "fgh"]`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and x IN ["abc", "cde", "fgh"]`,
			input: obj{
				"x": abc,
				"y": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and (x IN ["abc","cde","fgh"])`,
			input: obj{
				"x": cde,
				"y": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and not (x IN ["abc","cde","fgh"])`,
			input: obj{
				"x": abc,
				"y": 5,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x IN ["abc", "cde", "fgh"]`,
			input: obj{
				"x": "xyz",
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestListOfDoubles(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: `x IN [1.1, 2.2, 3.3]`,
			input: obj{
				"x": 1.1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and x IN [1.1, 2.1, 3.3]`,
			input: obj{
				"x": 1.1,
				"y": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and (x IN [1.4, 2.1, 3.3])`,
			input: obj{
				"x": 1.4,
				"y": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and not (x IN [1.1, 2.2, 3.3])`,
			input: obj{
				"x": 1.1,
				"y": 5,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x IN [1.0, 2.2, 3.3]`,
			input: obj{
				"x": 4.0,
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.False(t, Evaluate(tt.rule, obj{"x": abc}), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestListOfInts(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: `x IN [1, 2, 3]`,
			input: obj{
				"x": 1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and x IN [1, 2, 3]`,
			input: obj{
				"x": 1,
				"y": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and (x IN [1, 2, 3])`,
			input: obj{
				"x": 1,
				"y": 5,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `y eq 5 and not (x IN [1, 2, 3])`,
			input: obj{
				"x": 1,
				"y": 5,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x IN [1, 2, 3]`,
			input: obj{
				"x": 4,
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.False(t, Evaluate(tt.rule, obj{"x": "123"}), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestPresent(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: `x pr`,
			input: obj{
				"x": true,
			},
			result:   true,
			hasError: false,
		},
		{
			rule:     `x pr`,
			input:    obj{},
			result:   false,
			hasError: false,
		},
		{
			rule: `not (x pr)`,
			input: obj{
				"x": true,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `NOT (x pr)`,
			input: obj{
				"x": true,
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestNull(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: xEqNull,
			input: obj{
				"x": true,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x EQ null`,
			input: obj{
				"x": true,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne null`,
			input: obj{
				"x": true,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xEqNull,
			input: obj{
				"x": nil,
			},
			result:   true,
			hasError: false,
		},
		{
			rule:     xEqNull,
			input:    obj{},
			result:   true,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestNullInvalids(t *testing.T) {
	t.Parallel()

	badRules := []string{
		`x lt null`,
		`x le null`,
		`x ge null`,
		`x gt null`,
		`x co null`,
		`x sw null`,
		`x ew null`,
		`x in null`,
	}
	for _, rule := range badRules {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()

			_, err := eval(rule, obj{
				"x": nil,
			})
			assert.Error(t, err)
		})
	}
}

func TestBool(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: `x eq true`,
			input: obj{
				"x": true,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x ne true`,
			input: obj{
				"x": false,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x eq true`,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne true`,
			input: obj{
				"x": 2,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x eq false`,
			input: obj{
				"x": true,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `xy eq true`,
			input: obj{
				"x": true,
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input))
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestBoolInvalids(t *testing.T) {
	t.Parallel()

	badRules := []string{
		`x le true`,
		`x ge true`,
		`x gt true`,
		`x lt true`,
		`x le true`,
		`x co true`,
		`x sw true`,
		`x ew true`,
		`x in true`,
	}

	for _, rule := range badRules {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()

			result, err := eval(rule, obj{
				"x": true,
			})
			require.Error(t, err)
			assert.False(t, result)
		})
	}
}

func TestIntInvalids(t *testing.T) {
	t.Parallel()

	badRules := []string{
		`x co 1`,
		`x sw 1`,
		`x ew 1`,
	}

	for _, rule := range badRules {
		t.Run(rule, func(t *testing.T) {
			t.Parallel()

			result, err := eval(rule, obj{
				"x": 2,
			})
			require.Error(t, err)
			assert.False(t, result)
		})
	}
}

func TestInt(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: xEq1,
			input: obj{
				"x": 1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x == 1`,
			input: obj{
				"x": 1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xEq1,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xEq1,
			input: obj{
				"y": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne 1`,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: x1,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne 1`,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: x1,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x NE 1`,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xLe1,
			input: obj{
				"x": 0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x <= 1`,
			input: obj{
				"x": 0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLe1,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x LE 1`,
			input: obj{
				"x": 1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLe1,
			input: obj{
				"x": 2,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x LT 1`,
			input: obj{
				"x": 0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x < 1`,
			input: obj{
				"x": 0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x lt 1`,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x lt 1`,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x < 1`,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x GT 1`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x > 1`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x gt 1`,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x gt 1`,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x > 1`,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x GE 1`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x >= 1`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xGe1,
			input: obj{
				"x": 1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xGe1,
			input: obj{
				"x": "1",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xGe1,
			input: obj{
				"x": 0,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x >= 1`,
			input: obj{
				"x": 0,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x NE 1`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: x1,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xEq1,
			input: obj{
				"y": 1.0,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x == 1`,
			input: obj{
				"y": 1.0,
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
		})
	}
}

func TestFloat(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule: xEq10,
			input: obj{
				"x": 1.0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xEq10,
			input: obj{
				"y": 1.0,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne 1.1`,
			input: obj{
				"x": 1.1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xLe11,
			input: obj{
				"x": 0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLe11,
			input: obj{
				"x": 1.1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xLe11,
			input: obj{
				"x": 2.0,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x lt 1.1`,
			input: obj{
				"x": 0.0,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x lt 1.0`,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x gt 1.1`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x gt 1.0`,
			input: obj{
				"x": 1,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ge 1.2`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x ge 1.0`,
			input: obj{
				"x": 1,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x ge 1.0`,
			input: obj{
				"x": 0,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne 1.0`,
			input: obj{
				"x": 2,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xEq10,
			input: obj{
				"y": 1.0,
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
			assert.False(t, Evaluate(tt.rule, obj{"x": "2.4"}), tt.rule)
		})
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	tests := []testCase{
		{
			rule:     `x eq ""`,
			input:    obj{},
			result:   false,
			hasError: false,
		},
		{
			rule: `x eq ""`,
			input: obj{
				"x": "",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x eq "abc"`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x eq "abc"`,
			input: obj{
				"y": abc,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne "abc"`,
			input: obj{
				"x": abc,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x le "abc"`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x le "cde"`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x le "cde"`,
			input: obj{
				"x": "efg",
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x lt "cde"`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x lt "cde"`,
			input: obj{
				"x": cde,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x gt "cde"`,
			input: obj{
				"x": "def",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x gt "cde"`,
			input: obj{
				"x": abc,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: xGeCde,
			input: obj{
				"x": cde,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xGeCde,
			input: obj{
				"x": "def",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: xGeCde,
			input: obj{
				"x": abc,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ne "abc"`,
			input: obj{
				"x": cde,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x co "ab"`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x co "ab"`,
			input: obj{
				"x": bbc,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x CO "ab"`,
			input: obj{
				"x": bbc,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x sw "ab"`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x SW "ab"`,
			input: obj{
				"x": abc,
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x sw "ab"`,
			input: obj{
				"x": bbc,
			},
			result:   false,
			hasError: false,
		},
		{
			rule: `x ew "ab"`,
			input: obj{
				"x": "bab",
			},
			result:   true,
			hasError: false,
		},
		{
			rule: `x EW "ab"`,
			input: obj{
				"x": bbc,
			},
			result:   false,
			hasError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.rule, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.result, Evaluate(tt.rule, tt.input), tt.rule)
			assert.Equal(t, tt.result, Evaluate(fmt.Sprintf(s2, tt.rule), tt.input), tt.rule)
			assert.False(t, Evaluate(tt.rule, obj{"x": 3}), tt.rule)
		})
	}
}
