package linter

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

var (
	errInvalidSemaphoreConfiguration = errors.New("invalid Semaphore configuration")
)

// NewLintCmd returns the command that lints a flag configuration file.
func NewLintCmd() *cobra.Command {
	// Flag values are local to each command so several commands can exist (and run) at once.
	var lintFlagFormat string

	lintCmd := &cobra.Command{
		Use:   "lint <config_file>",
		Short: "🛑 Lint Semaphore configuration file.",
		Long:  `🛑 Validate Semaphore configuration file, it will return an error if your file is not valid.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLint(cmd, args, lintFlagFormat)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	lintCmd.Flags().
		StringVarP(&lintFlagFormat, "format", "f", "yaml", "Format of your input file (YAML, JSON or TOML)")

	return lintCmd
}

func runLint(cmd *cobra.Command, args []string, lintFlagFormat string) error {
	output := helper.Output{}

	l := Linter{
		InputFile:   extractFilePathFromArgs(args),
		InputFormat: lintFlagFormat,
	}
	if errs := l.Lint(); len(errs) > 0 {
		for _, err := range errs {
			output.Add(err.Error(), helper.ErrorLevel)
		}

		output.PrintLines(cmd)

		return errInvalidSemaphoreConfiguration
	}

	output.Add("Valid Semaphore configuration", helper.StatusLevel)
	output.PrintLines(cmd)

	return nil
}

func extractFilePathFromArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}

	return args[0]
}
