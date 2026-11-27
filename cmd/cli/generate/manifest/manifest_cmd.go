package manifest

import (
	"fmt"
	"reflect"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
)

// HelpVars returns the kong interpolation variables used by [Cmd] help tags.
func HelpVars() kong.Vars {
	return kong.Vars{
		"helpFormat": "Format of your input file (YAML, JSON or TOML)",
		"helpCfg":    "Location of your Semaphore local configuration file",
		"helpDest": "Destination of your flag manifest file. " +
			"If not provided, the manifest will be printed to the console.",
	}
}

// Cmd generates an OpenFeature flag manifest from a flag configuration file.
type Cmd struct {
	Format                  string `default:"yaml"     help:"${helpFormat}"             name:"format"   short:"f"`
	Config                  string `help:"${helpCfg}"  name:"config"                    short:"c"`
	FlagManifestDestination string `help:"${helpDest}" name:"flag_manifest_destination" required:"true"`
}

// Help returns the detailed description of generate manifest for kong's help output.
func (Cmd) Help() string {
	return "📄 (experimental) Generate an OpenFeature flag manifest based on your flags configuration. " +
		"⚠️ note that this is an experimental feature and we may change this command line without warning."
}

// Run generates the flag manifest and writes diagnostics to streams.
func (c Cmd) Run(streams *helper.Streams) error {
	m, err := NewManifest(c.Config, c.Format, c.FlagManifestDestination)
	if err != nil {
		return err
	}

	output, err := m.Generate()
	if err != nil {
		return err
	}

	err = output.PrintLines(streams.Stdout, streams.Stderr)
	if err != nil {
		return fmt.Errorf("print manifest output: %w", err)
	}

	return nil
}

// Run parses args against [Cmd] and executes manifest generation with injected output streams.
func Run(args []string, streams *helper.Streams) error {
	var runner struct {
		Cmd

		Help helper.HelpFlag `help:"Show context-sensitive help." name:"help" short:"h"`
	}

	parser, err := kong.New(&runner,
		kong.Name("manifest"),
		kong.Description("📄 (experimental) Generate an OpenFeature flag manifest based on your flags configuration."),
		kong.NoDefaultHelp(),
		kong.KindMapper(reflect.String, kong.MapperFunc(helper.DecodeString)),
		kong.Writers(streams.Stdout, streams.Stderr),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helper.HelpWidth}),
		HelpVars(),
	)
	if err != nil {
		return fmt.Errorf("build manifest parser: %w", err)
	}

	kctx, exited, err := helper.Parse(parser, args)
	if exited {
		return nil
	}

	if err != nil {
		return fmt.Errorf("%w: %w", helper.ErrUsage, err)
	}

	return kctx.Run(streams) //nolint:wrapcheck // Cmd.Run preserves original manifest error
}
