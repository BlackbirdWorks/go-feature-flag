package cliapp

import (
	"context"
	"crypto/fips140"
	"fmt"
	"io"
	"maps"

	"github.com/alecthomas/kong"
	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/config"
	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
	"github.com/blackbirdworks/semaphore/cmd/cli/generate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/linter"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

const (
	helpWidth    = 80
	configCmdKey = "config"
)

// Build is what the linker stamps into the binary (-X main.version=...).
type Build = buildinfo.Linked

func versionText(build Build) string {
	return buildinfo.Text("semaphore-cli", build, buildinfo.Read(), fips140.Enabled())
}

type versionFlag bool

func (versionFlag) BeforeReset(app *kong.Kong, vars kong.Vars) error {
	_, err := fmt.Fprint(app.Stdout, vars["version"])
	if err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	app.Exit(0)

	return nil
}

type evaluateCmd struct {
	Args []string `arg:"" optional:""`
}

func (c *evaluateCmd) Run(streams *helper.Streams) error {
	return runCobra(context.Background(), evaluate.NewEvaluateCmd(), c.Args, streams)
}

type lintCmd struct {
	Args []string `arg:"" optional:""`
}

func (c *lintCmd) Run(streams *helper.Streams) error {
	return runCobra(context.Background(), linter.NewLintCmd(), c.Args, streams)
}

type generateCmd struct {
	Args []string `arg:"" optional:""`
}

func (c *generateCmd) Run(streams *helper.Streams) error {
	return runCobra(context.Background(), generate.NewGenerateCmd(), c.Args, streams)
}

type publishCmd struct {
	Args []string `arg:"" optional:""`
}

func (c *publishCmd) Run(ctx context.Context, streams *helper.Streams, baseDeps *publish.Deps) error {
	deps := *baseDeps
	deps.Args = func() []string { return append([]string{"publish"}, c.Args...) }

	//nolint:contextcheck // ctx is propagated into Cobra via root.ExecuteContext(ctx)
	return runCobra(ctx, publish.NewPublishCmdWith(deps), c.Args, streams)
}

func runCobra(ctx context.Context, cmd *cobra.Command, args []string, streams *helper.Streams) error {
	root := &cobra.Command{Use: "semaphore-cli", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(streams.Stdout)
	root.SetErr(streams.Stderr)
	root.AddCommand(cmd)
	root.SetArgs(append([]string{cmd.Name()}, args...))

	return root.ExecuteContext(ctx) //nolint:wrapcheck // cobra command errors carry their own exit codes
}

type cli struct {
	Evaluate evaluateCmd `cmd:""                            help:"${helpEvaluate}" passthrough:""`
	Lint     lintCmd     `cmd:""                            help:"${helpLint}"     passthrough:""`
	Generate generateCmd `cmd:""                            help:"${helpGenerate}" passthrough:""`
	Publish  publishCmd  `cmd:""                            help:"${helpPublish}"  passthrough:""`
	Config   config.Cmd  `cmd:""                            help:"${helpConfig}"`
	Version  versionFlag `help:"Print the version and exit" name:"version"         short:"v"`
}

func helpVars(build Build) kong.Vars {
	vars := config.HelpVars()
	maps.Copy(vars, kong.Vars{
		"version":      versionText(build),
		"helpEvaluate": "⚙️ Evaluate feature flags based on configuration and context",
		"helpLint":     "🛑 Lint Semaphore configuration file.",
		"helpGenerate": "🏗️ Generate Semaphore related files",
		"helpPublish":  "Publish flag files and flag-set documents to S3 behind a manifest",
		"helpConfig":   "Validate and print the version 2 bootstrap file of the relay proxy",
	})

	return vars
}

type exitSignal struct {
	code int
}

func newParser(command *cli, stdout, stderr io.Writer, build Build) (*kong.Kong, error) {
	parser, err := kong.New(command,
		kong.Name("semaphore-cli"),
		kong.Description("A command line interface for Semaphore operations."),
		kong.Writers(stdout, stderr),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helpWidth}),
		helpVars(build),
		kong.Exit(func(code int) { panic(exitSignal{code: code}) }),
	)
	if err != nil {
		return nil, fmt.Errorf("build parser: %w", err)
	}

	return parser, nil
}
