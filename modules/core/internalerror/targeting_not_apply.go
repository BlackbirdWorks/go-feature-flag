package internalerror

import (
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

// RuleNotApplyError reports that a rule does not apply to the evaluation context.
type RuleNotApplyError struct {
	Context ffcontext.Context
}

func (m *RuleNotApplyError) Error() string {
	return "Rule does not apply for this user " + m.Context.GetKey()
}
