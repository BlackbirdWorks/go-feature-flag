package linter

import (
	"fmt"

	clihelper "github.com/blackbirdworks/semaphore/cmd/cli/helper"
	helper "github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
)

// Linter checks a flag configuration file for errors.
type Linter struct {
	InputFile   string
	InputFormat string
}

// Lint returns the errors found in the flag configuration file.
func (l *Linter) Lint() []error {
	flags, err := helper.LoadConfigFile(
		l.InputFile,
		l.InputFormat,
		helper.DefaultLocations(),
		clihelper.LegacyWarner(),
	)
	if err != nil {
		return []error{err}
	}

	errs := make([]error, 0)
	for _, flagErr := range helper.ValidateFlags(flags) {
		errs = append(errs, fmt.Errorf("%s: %w", l.InputFile, flagErr))
	}

	return errs
}
