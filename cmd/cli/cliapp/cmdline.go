package cliapp

import (
	"crypto/fips140"
	"fmt"
	"io"
	"maps"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/config"
	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
	"github.com/blackbirdworks/semaphore/cmd/cli/generate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/linter"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

const (
	configCmdKey  = "config"
	publishCmdKey = "publish"
)

// Build is what the linker stamps into the binary (-X main.version=...).
type Build = buildinfo.Linked

func versionText(build Build) string {
	return buildinfo.Text("semaphore-cli", build, buildinfo.Read(), fips140.Enabled())
}

type versionFlag bool

func (versionFlag) BeforeReset(ctx *kong.Context, vars kong.Vars) error {
	for _, p := range ctx.Path {
		if p.Flag == nil || p.Flag.Name != "version" {
			continue
		}

		val, ok := ctx.FlagValue(p.Flag).(versionFlag)
		if ok && bool(val) {
			_, err := fmt.Fprint(ctx.Stdout, vars["version"])
			if err != nil {
				return fmt.Errorf("write version: %w", err)
			}

			return helper.ErrEarlyExit
		}
	}

	return nil
}

//nolint:govet // fieldalignment: field order sets kong help command order
type cli struct {
	Evaluate evaluate.Cmd    `cmd:""                              help:"${helpEvaluate}"`
	Lint     linter.Cmd      `cmd:""                              help:"${helpLint}"`
	Generate generate.Cmd    `cmd:""                              help:"${helpGenerate}"`
	Publish  publish.Cmd     `cmd:""                              help:"${helpPublish}"`
	Config   config.Cmd      `cmd:""                              help:"${helpConfig}"`
	Help     helper.HelpFlag `help:"Show context-sensitive help." name:"help"            short:"h"`
	Version  versionFlag     `help:"Print the version and exit"   name:"version"         short:"v"`
}

func helpVars(build Build) kong.Vars {
	vars := kong.Vars{
		"version":      versionText(build),
		"helpEvaluate": "⚙️ Evaluate feature flags based on configuration and context",
		"helpLint":     "🛑 Lint Semaphore configuration file.",
		"helpGenerate": "🏗️ Generate Semaphore related files",
		"helpPublish":  "Publish flag files and flag-set documents to S3 behind a manifest",
		"helpConfig":   "Validate and print the version 2 bootstrap file of the relay proxy",
	}
	maps.Copy(vars, config.HelpVars())
	maps.Copy(vars, evaluate.HelpVars())
	maps.Copy(vars, generate.HelpVars())
	maps.Copy(vars, linter.HelpVars())
	maps.Copy(vars, publish.HelpVars())

	return vars
}

func newParser(command *cli, stdout, stderr io.Writer, build Build) (*kong.Kong, error) {
	parser, err := kong.New(command,
		kong.Name("semaphore-cli"),
		kong.Description("A command line interface for Semaphore operations."),
		kong.NoDefaultHelp(),
		kong.PostBuild(helper.RegisterHelpFlag),
		kong.Writers(stdout, stderr),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helper.HelpWidth}),
		helpVars(build),
	)
	if err != nil {
		return nil, fmt.Errorf("build parser: %w", err)
	}

	return parser, nil
}
