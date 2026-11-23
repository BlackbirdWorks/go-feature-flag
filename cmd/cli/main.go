package main

import (
	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
	"github.com/blackbirdworks/semaphore/cmd/cli/generate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/linter"
	"github.com/spf13/cobra"
)

func main() {
	if err := initRootCmd().Execute(); err != nil {
		helper.PrintFatalAndExit(err)
	}
}

func initRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "go-feature-flag-cli",
		Short: "GO Feature Flag CLI tool",
		Long:  `A command line interface for GO Feature Flag operations.`,
	}
	rootCmd.AddCommand(evaluate.NewEvaluateCmd())
	rootCmd.AddCommand(linter.NewLintCmd())
	rootCmd.AddCommand(generate.NewGenerateCmd())
	return rootCmd
}
