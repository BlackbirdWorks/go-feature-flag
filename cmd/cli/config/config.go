package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The values of --output.
const (
	outputText = "text"
	outputJSON = "json"
)

// HelpVars returns the kong interpolation variables used by [Cmd] help tags.
func HelpVars() kong.Vars {
	return kong.Vars{
		"helpConfigOutput":   "output format: text or json (YAML for print)",
		"helpConfigValidate": "Check a bootstrap file with the loader of the relay proxy",
		"helpConfigPrint": "Print the effective bootstrap file: defaults and environment overrides applied, " +
			"secrets redacted",
	}
}

// Deps are the parts of the outside world that the commands use.
type Deps struct {
	// LookupEnv reads an environment variable: the SEMAPHORE_* overrides apply to the effective configuration.
	LookupEnv func(string) (string, bool)
	// ReadFile reads the bootstrap file.
	ReadFile func(string) ([]byte, error)
}

// DefaultDeps returns the dependencies of the real command.
func DefaultDeps() Deps { return Deps{LookupEnv: os.LookupEnv, ReadFile: os.ReadFile} }

// Cmd is the kong command group for validating and printing the version 2 bootstrap file of the relay proxy.
type Cmd struct {
	Output   helper.OutputFlag `default:"text" help:"${helpConfigOutput}"   name:"output" short:"o"`
	Validate ValidateCmd       `cmd:""         help:"${helpConfigValidate}"`
	Print    PrintCmd          `cmd:""         help:"${helpConfigPrint}"`
}

// ValidateCmd checks a bootstrap file with the loader of the relay proxy.
type ValidateCmd struct {
	File string `arg:"" help:"Bootstrap file to check." name:"file"`
}

// Help returns the detailed description of config validate for kong's help output.
func (ValidateCmd) Help() string {
	return `Reads <file> with the strict loader of the relay proxy: version 2 required, unknown keys, bad references and
bad values are errors that name the file, the path of the key and the line. Secret references are checked for their
shape, not resolved. Exit code 0 when valid, 2 when invalid, 1 on another failure.`
}

// Run validates the bootstrap file and writes the result to streams.Stdout.
func (v ValidateCmd) Run(parent *Cmd, streams *helper.Streams, deps *Deps) error {
	out := string(parent.Output)

	err := helper.ReportJSONError(
		streams.Stdout,
		out == outputJSON,
		nil,
		runValidate(streams.Stdout, v.File, out, deps),
	)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	return nil
}

// PrintCmd prints the effective bootstrap configuration with defaults and environment overrides applied.
type PrintCmd struct {
	File      string `arg:""                                              help:"Bootstrap file to print." name:"file"`
	Effective bool   `help:"print the effective configuration (required)" name:"effective"`
}

// Help returns the detailed description of config print for kong's help output.
func (PrintCmd) Help() string {
	return `Prints what the relay proxy would run with: the file after the SEMAPHORE_* environment overrides and the
defaults. Secrets, API keys and admin keys print as [REDACTED]; references (fromEnv, fromFile) print as written.
--effective is required. Exit code 2 when the file is invalid.`
}

// Run prints the effective bootstrap file to streams.Stdout.
func (p PrintCmd) Run(parent *Cmd, streams *helper.Streams, deps *Deps) error {
	out := string(parent.Output)

	var err error
	if !p.Effective {
		err = fmt.Errorf("%w: --effective is required", helper.ErrUsage)
	} else {
		err = runPrint(streams.Stdout, p.File, out, deps)
	}

	repErr := helper.ReportJSONError(streams.Stdout, out == outputJSON, nil, err)
	if repErr != nil {
		return fmt.Errorf("%w", repErr)
	}

	return nil
}

// Run parses args against the config command tree and executes the selected subcommand with injected streams and deps.
// Running without a subcommand prints config help to streams.Stdout and returns nil.
func Run(args []string, streams *helper.Streams, deps Deps) error {
	var runner struct {
		Cmd

		Help helper.HelpFlag `help:"Show context-sensitive help." name:"help" short:"h"`
	}

	parser, err := kong.New(&runner,
		kong.Name("config"),
		kong.Description("Validate and print the version 2 bootstrap file of the relay proxy"),
		kong.NoDefaultHelp(),
		kong.Writers(streams.Stdout, streams.Stderr),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helper.HelpWidth}),
		HelpVars(),
	)
	if err != nil {
		return fmt.Errorf("build config parser: %w", err)
	}

	if len(args) == 0 {
		traceCtx, traceErr := kong.Trace(parser, nil)
		if traceErr != nil {
			return fmt.Errorf("trace config help: %w", traceErr)
		}

		return traceCtx.PrintUsage(false) //nolint:wrapcheck // PrintUsage writes directly to stdout
	}

	kctx, exited, err := helper.Parse(parser, args)
	if exited {
		return nil
	}

	if err != nil {
		return handleParseError(err, args, streams)
	}

	//nolint:wrapcheck // subcommand Run already formats and wraps its error
	return kctx.Run(&runner.Cmd, streams, &deps)
}

func handleParseError(err error, args []string, streams *helper.Streams) error {
	var parseErr *kong.ParseError
	if !errors.As(err, &parseErr) {
		return fmt.Errorf("%w: %w", helper.ErrUsage, err)
	}

	if parseErr.Context.Selected() == nil && strings.HasPrefix(parseErr.Error(), "expected one of ") {
		return parseErr.Context.PrintUsage(false) //nolint:wrapcheck // PrintUsage writes directly to stdout
	}

	val, found := helper.ScanOutputFlag(parseErr.Context.Flags(), args)

	repErr := helper.ReportJSONError(
		streams.Stdout,
		found && val == outputJSON,
		nil,
		fmt.Errorf("%w: %w", helper.ErrUsage, err),
	)
	if repErr != nil {
		return fmt.Errorf("%w", repErr)
	}

	return nil
}

// load reads and validates the file the way the relay does.
func load(file string, deps *Deps) (*bootstrap.Config, error) {
	cfg, err := bootstrap.Load(&bootstrap.Overrides{Config: file}, bootstrap.LoadOptions{
		LookupEnv: deps.LookupEnv, ReadFile: deps.ReadFile,
	})
	if err != nil {
		code := helper.ExitFailure
		if _, ok := errors.AsType[*bootstrap.Error](err); ok {
			code = helper.ExitInvalid
		}

		return nil, &helper.ExitError{Err: fmt.Errorf("%s: %w", file, err), Code: code}
	}

	return cfg, nil
}

func checkOutput(format string) error {
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: --output must be text or json, not %q", helper.ErrUsage, format)
	}

	return nil
}

// jsonValidation is the JSON output of validate.
//
//nolint:govet // field order is the order of the JSON output
type jsonValidation struct {
	File  string     `json:"file"`
	Valid bool       `json:"valid"`
	Error *jsonError `json:"error,omitempty"`
}

// jsonError is the first error of an invalid file.
//
//nolint:govet // field order is the order of the JSON output
type jsonError struct {
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
}

func runValidate(w io.Writer, file, output string, deps *Deps) error {
	err := checkOutput(output)
	if err != nil {
		return err
	}

	_, err = load(file, deps)
	if err == nil {
		return printValidation(w, output, &jsonValidation{File: file, Valid: true}, file+": valid\n")
	}

	report := &jsonValidation{File: file, Error: &jsonError{Message: err.Error()}}
	if bootErr, ok := errors.AsType[*bootstrap.Error](err); ok {
		report.Error.Path, report.Error.Line = bootErr.Path, bootErr.Line
	}

	if output == outputJSON {
		printErr := printValidation(w, output, report, "")
		if printErr != nil {
			return printErr
		}

		return &helper.ExitError{
			Err:  helper.MarkReported(err),
			Code: helper.ExitCodeOf(err),
		} // the report is the JSON document
	}

	return err
}

func printValidation(w io.Writer, output string, report *jsonValidation, text string) error {
	if output == outputJSON {
		return writeJSON(w, report)
	}

	_, err := io.WriteString(w, text)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	err := enc.Encode(v)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

func runPrint(w io.Writer, file, output string, deps *Deps) error {
	err := checkOutput(output)
	if err != nil {
		return err
	}

	cfg, err := load(file, deps)
	if err != nil {
		return err
	}

	if output == outputJSON {
		return writeJSON(w, cfg)
	}

	return cfg.Print(w) //nolint:wrapcheck // Print wraps its own errors
}
