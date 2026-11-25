package main

import (
	"crypto/fips140"

	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
	"github.com/blackbirdworks/semaphore/cmd/cli/generate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/linter"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

// version, commit and date are overridden by GoReleaser during the build (-X main.version=...).
var (
	version = buildinfo.DevVersion
	commit  string //nolint:gochecknoglobals // set with -ldflags -X main.commit
	date    string //nolint:gochecknoglobals // set with -ldflags -X main.date
)

func main() {
	err := initRootCmd().Execute()
	if err != nil {
		helper.PrintFatalAndExit(err)
	}
}

func initRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "semaphore-cli",
		Short: "Semaphore CLI tool",
		Long:  `A command line interface for Semaphore operations.`,
		// Setting Version makes cobra register --version (and -v); the template is the whole output.
		Version: version,
	}
	rootCmd.SetVersionTemplate(buildinfo.Text("semaphore-cli",
		buildinfo.Linked{Version: version, Commit: commit, Date: date}, buildinfo.Read(), fips140.Enabled()))
	rootCmd.AddCommand(evaluate.NewEvaluateCmd())
	rootCmd.AddCommand(linter.NewLintCmd())
	rootCmd.AddCommand(generate.NewGenerateCmd())

	return rootCmd
}
