package linter

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

var errInvalidSemaphoreConfiguration = errors.New("invalid Semaphore configuration")

// HelpVars returns the kong interpolation variables used by [Cmd] help tags.
func HelpVars() kong.Vars {
	return kong.Vars{
		"helpLintConfig": "Path to your Semaphore configuration file.",
		"helpLintFormat": "Format of your input file (YAML, JSON or TOML)",
	}
}

// Cmd lints a Semaphore flag configuration file.
type Cmd struct {
	ConfigFile string `arg:""         help:"${helpLintConfig}" name:"config_file" optional:""`
	Format     string `default:"yaml" help:"${helpLintFormat}" name:"format"      short:"f"`
}

// Help returns the detailed description of lint for kong's help output.
func (Cmd) Help() string {
	return "🛑 Validate Semaphore configuration file, it will return an error if your file is not valid."
}

// Run executes the lint command using streams for stdout and stderr.
func (c Cmd) Run(streams *helper.Streams) error {
	return runLint(streams, c.ConfigFile, c.Format)
}

// Run parses args against [Cmd] and executes the linter with injected output streams.
func Run(args []string, streams *helper.Streams) error {
	var runner struct {
		Cmd

		Help helper.HelpFlag `help:"Show context-sensitive help." name:"help" short:"h"`
	}

	parser, err := kong.New(&runner,
		kong.Name("lint"),
		kong.Description("🛑 Lint Semaphore configuration file."),
		kong.NoDefaultHelp(),
		kong.KindMapper(reflect.String, kong.MapperFunc(helper.DecodeString)),
		kong.Writers(streams.Stdout, streams.Stderr),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helper.HelpWidth}),
		HelpVars(),
	)
	if err != nil {
		return fmt.Errorf("build lint parser: %w", err)
	}

	kctx, exited, err := helper.Parse(parser, args)
	if exited {
		return nil
	}

	if err != nil {
		return fmt.Errorf("%w: %w", helper.ErrUsage, err)
	}

	return kctx.Run(streams) //nolint:wrapcheck // Cmd.Run returns package sentinel or nil
}

func runLint(streams *helper.Streams, inputFile, format string) error {
	output := helper.Output{}

	l := Linter{
		InputFile:   inputFile,
		InputFormat: format,
	}
	if errs := l.Lint(); len(errs) > 0 {
		for _, err := range errs {
			output.Add(err.Error(), helper.ErrorLevel)
		}

		printErr := output.PrintLines(streams.Stdout, streams.Stderr)
		if printErr != nil {
			return fmt.Errorf("print lint findings: %w", printErr)
		}

		return errInvalidSemaphoreConfiguration
	}

	output.Add("Valid Semaphore configuration", helper.StatusLevel)

	err := output.PrintLines(streams.Stdout, streams.Stderr)
	if err != nil {
		return fmt.Errorf("print lint status: %w", err)
	}

	return nil
}
