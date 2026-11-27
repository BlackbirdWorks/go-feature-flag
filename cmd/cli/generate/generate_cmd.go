package generate

import (
	"fmt"
	"maps"
	"reflect"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

// HelpVars returns the kong interpolation variables used by [Cmd] and its subcommands.
func HelpVars() kong.Vars {
	vars := manifest.HelpVars()
	maps.Copy(vars, kong.Vars{
		"helpGenerateManifest": "📄 (experimental) Generate an OpenFeature flag manifest based on your flags configuration.",
	})

	return vars
}

// Cmd is the kong command group that generates Semaphore related files.
type Cmd struct {
	Manifest manifest.Cmd `cmd:"" help:"${helpGenerateManifest}"`
}

// Run parses args against [Cmd] and executes the selected subcommand with injected output streams.
func Run(args []string, streams *helper.Streams) error {
	var runner struct {
		Cmd

		Help helper.HelpFlag `help:"Show context-sensitive help." name:"help" short:"h"`
	}

	parser, err := kong.New(&runner,
		kong.Name("generate"),
		kong.Description("🏗️ Generate Semaphore relates files (examples: flag manifest, ...)"),
		kong.NoDefaultHelp(),
		kong.KindMapper(reflect.String, kong.MapperFunc(helper.DecodeString)),
		kong.Writers(streams.Stdout, streams.Stderr),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helper.HelpWidth}),
		HelpVars(),
	)
	if err != nil {
		return fmt.Errorf("build generate parser: %w", err)
	}

	kctx, exited, err := helper.Parse(parser, args)
	if exited {
		return nil
	}

	if err != nil {
		return fmt.Errorf("%w: %w", helper.ErrUsage, err)
	}

	return kctx.Run(streams) //nolint:wrapcheck // subcommand Run returns user-facing error
}
