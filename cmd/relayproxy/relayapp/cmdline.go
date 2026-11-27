package relayapp

import (
	"crypto/fips140"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmdhelpers/buildinfo"
)

// errUsage is wrapped by the error of command line flags that do not parse.
var errUsage = errors.New("invalid command line")

// Build is what the linker stamps into the binary (-X main.version=...).
type Build = buildinfo.Linked

// versionText is what --version prints.
func versionText(build Build) string {
	return buildinfo.Text("semaphore", build, buildinfo.Read(), fips140.Enabled())
}

// stringFlag is a string flag that knows whether it was given: an explicit empty value (--state-dir=) is given, and
// differs from no flag. Its value is the next argument whatever it starts with (--config -a.yaml).
type stringFlag struct {
	Value string
	Given bool
}

// Decode implements [kong.MapperValue].
func (o *stringFlag) Decode(ctx *kong.DecodeContext) error {
	token := ctx.Scan.Pop()
	if token.IsEOL() {
		return fmt.Errorf("%w: %s needs a value", errUsage, ctx.Value.Summary())
	}

	o.Value, o.Given = token.String(), true

	return nil
}

// cli is the command line of the relay: the override flags, --config, --help and --version. A flag wins over its
// environment variable, which wins over the file; the environment is read by [bootstrap.Load], so the variables are
// named in the help of the flags.
type cli struct {
	StateDir    stringFlag `help:"${helpStateDir}"            name:"state-dir"    placeholder:"DIR"`
	Config      stringFlag `help:"${helpConfig}"              name:"config"       placeholder:"FILE"`
	LogLevel    stringFlag `help:"${helpLogLevel}"            name:"log-level"    placeholder:"LEVEL"`
	LogFormat   stringFlag `help:"${helpLogFormat}"           name:"log-format"   placeholder:"FORMAT"`
	Listen      stringFlag `help:"${helpListen}"              name:"listen"       placeholder:"ADDR"`
	OpsListen   stringFlag `help:"${helpOpsListen}"           name:"ops-listen"   placeholder:"ADDR"`
	ManifestKey stringFlag `help:"${helpManifestKey}"         name:"manifest-key" placeholder:"KEY"`
	Version     bool       `help:"Print the version and exit" name:"version"      short:"v"`
	// Help is declared, so that the parse reads the whole command line (an unknown flag after --help is an error)
	// before the help is answered; --help wins over --version.
	Help bool `help:"Print this help and exit (wins over --version)" name:"help" short:"h"`
}

// helpWidth is the width at which the help text wraps.
const helpWidth = 100

// helpVars are the help texts of the flags: each names the setting and its environment variable.
func helpVars() kong.Vars {
	return kong.Vars{
		"helpStateDir":    "controlPlane.stateDir, empty for no state directory (or " + bootstrap.EnvStateDir + ")",
		"helpConfig":      "Location of the version 2 bootstrap file (or " + bootstrap.EnvConfig + ")",
		"helpLogLevel":    "observability.logging.level (or " + bootstrap.EnvLogLevel + ")",
		"helpLogFormat":   "observability.logging.format (or " + bootstrap.EnvLogFormat + ")",
		"helpListen":      "Address of the api listener (or " + bootstrap.EnvListen + ")",
		"helpOpsListen":   "Address of the ops listener (or " + bootstrap.EnvOpsListen + ")",
		"helpManifestKey": "controlPlane.manifest.key (or " + bootstrap.EnvManifestKey + ")",
	}
}

// commandLine is what the command line says: the overrides of the file, and the two flags that answer without
// starting the relay.
type commandLine struct {
	Overrides *bootstrap.Overrides
	// Help is --help: it wins over Version when both are true.
	Help bool
	// Version is --version.
	Version bool
}

// newParser builds the kong parser of the relay; its output goes to out.
func newParser(command *cli, out io.Writer) (*kong.Kong, error) {
	parser, err := kong.New(command,
		kong.Name("semaphore"),
		kong.Description("Semaphore relay proxy: serves feature flags over OFREP from a version 2 bootstrap file."),
		kong.NoDefaultHelp(),
		kong.Writers(out, out),
		kong.Exit(func(int) {}),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helpWidth}),
		helpVars(),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	return parser, nil
}

// parseCommandLine parses the whole command line once, strictly: an unknown flag is an error whatever else is given.
// The overrides are the flags that override the file (a flag wins over the environment, which wins over the file).
func parseCommandLine(args []string) (*commandLine, error) {
	var command cli

	parser, err := newParser(&command, io.Discard)
	if err != nil {
		return nil, err
	}

	_, err = parser.Parse(args)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUsage, err)
	}

	return &commandLine{
		Help: command.Help, Version: command.Version,
		Overrides: &bootstrap.Overrides{
			Config: command.Config.Value, LogLevel: command.LogLevel.Value, LogFormat: command.LogFormat.Value,
			Listen: command.Listen.Value, OpsListen: command.OpsListen.Value, ManifestKey: command.ManifestKey.Value,
			StateDir: command.StateDir.Value, StateDirGiven: command.StateDir.Given,
		},
	}, nil
}

// usageText is what --help prints: every flag with its environment variable.
func usageText() string {
	var (
		command cli
		out     strings.Builder
	)

	parser, err := newParser(&command, &out)
	if err != nil {
		return err.Error()
	}

	ctx, err := kong.Trace(parser, nil)
	if err != nil {
		return err.Error()
	}

	_ = ctx.PrintUsage(false)

	return out.String()
}
