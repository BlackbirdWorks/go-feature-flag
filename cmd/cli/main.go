package main

import (
	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
	"github.com/blackbirdworks/semaphore/cmd/cli/generate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/linter"
)

func main() {
	if err := initRootCmd().Execute(); err != nil {
		helper.PrintFatalAndExit(err)
	}
}

func initRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "semaphore-cli",
		Short: "Semaphore CLI tool",
		Long:  `A command line interface for Semaphore operations.`,
	}
	rootCmd.AddCommand(evaluate.NewEvaluateCmd())
	rootCmd.AddCommand(linter.NewLintCmd())
	rootCmd.AddCommand(generate.NewGenerateCmd())
	return rootCmd
}
