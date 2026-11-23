package linter

import (
	"fmt"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/spf13/cobra"
)

var lintFlagFormat string

func NewLintCmd() *cobra.Command {
	lintCmd := &cobra.Command{
		Use:   "lint <config_file>",
		Short: "🛑 Lint GO Feature Flag configuration file.",
		Long:  `🛑 Validate GO Feature Flag configuration file, it will return an error if your file is not valid.`,
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
		return fmt.Errorf("invalid GO Feature Flag configuration")
	}

	output.Add("Valid GO Feature Flag configuration", helper.InfoLevel)
	output.PrintLines(cmd)
	return nil
}

func extractFilePathFromArgs(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
