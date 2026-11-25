package fixtures

// Rule names and targeting queries shared by the tests of several packages.
const (
	// DefaultMode is the rule name or targeting query "default mode".
	DefaultMode = "default mode"
	// FlagsetMode is the rule name or targeting query "flagset mode".
	FlagsetMode = "flagset mode"
	// QueryKeyEqKey is the rule name or targeting query `key eq "key"`.
	QueryKeyEqKey = `key eq "key"`
	// QueryKeyEqRandomKey is the rule name or targeting query `key eq "random-key"`.
	QueryKeyEqRandomKey = `key eq "random-key"`
	// QueryXEq is the rule name or targeting query "x eq ".
	QueryXEq = "x eq "
	// RuleDefault is the rule name or targeting query "defaultRule".
	RuleDefault = "defaultRule"
	// RuleOne is the rule name or targeting query "rule1".
	RuleOne = "rule1"
	// RuleTwo is the rule name or targeting query "rule2".
	RuleTwo = "rule2"
	// StepFormat is the rule name or targeting query "step %d".
	StepFormat = "step %d"
)
