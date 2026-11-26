package main

import (
	"crypto/fips140"
	"os"

	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/config"
	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
	"github.com/blackbirdworks/semaphore/cmd/cli/generate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/linter"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

// version, commit and date are overridden by GoReleaser during the build (-X main.version=...).
var (
	version = buildinfo.DevVersion
	commit  string //nolint:gochecknoglobals // set with -ldflags -X main.commit
	date    string //nolint:gochecknoglobals // set with -ldflags -X main.date
)

func main() {
	os.Exit(execute(os.Args[1:]))
}

// execute runs the command line and returns the exit code of the process. One stream rule holds for every command:
// stdout carries results only, stderr carries every diagnostic (Cobra's usage and errors, and the final error line).
// The exit code is 1 unless the command defines another one (2, 3, 4 or 5, see helper.ExitError).
func execute(args []string) int {
	cmd := initRootCmd()
	cmd.SetArgs(args)

	err := cmd.Execute()
	if err == nil {
		return 0
	}

	code := helper.ExitCodeOf(err)
	helper.PrintFatal(helper.FatalPrinter(os.Stderr), func(int) {}, err)

	return code
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
	rootCmd.AddCommand(publish.NewPublishCmd())
	rootCmd.AddCommand(config.NewConfigCmd())

	return rootCmd
}
