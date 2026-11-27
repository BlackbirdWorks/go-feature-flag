package cliapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/config"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/cli/publish"
)

type runOptions struct {
	Stdout    io.Writer
	Stderr    io.Writer
	LookupEnv func(string) (string, bool)
	ReadFile  func(string) ([]byte, error)
	Build     Build
	Args      []string
}

// Main runs the semaphore-cli command line with injected arguments, streams, environment lookup, and build metadata,
// returning the process exit code.
func Main(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	lookupEnv func(string) (string, bool),
	build Build,
) int {
	return run(ctx, &runOptions{
		Args:      args,
		Stdout:    stdout,
		Stderr:    stderr,
		LookupEnv: lookupEnv,
		ReadFile:  os.ReadFile,
		Build:     build,
	})
}

func run(ctx context.Context, opts *runOptions) int {
	code, err := execute(ctx, opts)
	if err == nil {
		return code
	}

	helper.PrintFatal(helper.FatalPrinter(opts.Stderr), func(int) {}, err)

	return helper.ExitCodeOf(err)
}

func execute(ctx context.Context, opts *runOptions) (int, error) {
	var command cli

	parser, err := newParser(&command, opts.Stdout, opts.Stderr, opts.Build)
	if err != nil {
		return helper.ExitFailure, err
	}

	if len(opts.Args) == 0 {
		traceCtx, traceErr := kong.Trace(parser, nil)
		if traceErr != nil {
			return helper.ExitFailure, fmt.Errorf("trace root help: %w", traceErr)
		}

		printErr := traceCtx.PrintUsage(false)
		if printErr != nil {
			return helper.ExitFailure, fmt.Errorf("print root help: %w", printErr)
		}

		return 0, nil
	}

	outcome, err := parseCatchingExit(parser, opts.Args)
	if outcome.exited {
		return outcome.exitCode, nil
	}

	if err != nil {
		return handleParseError(err, opts)
	}

	streams := helper.Streams{Stdout: opts.Stdout, Stderr: opts.Stderr}
	cfgDeps := config.Deps{LookupEnv: opts.LookupEnv, ReadFile: opts.ReadFile}
	pubDeps := publish.DefaultDeps()
	pubDeps.LookupEnv = opts.LookupEnv
	pubDeps.ReadFile = opts.ReadFile

	outcome.ctx.BindTo(ctx, (*context.Context)(nil))

	err = outcome.ctx.Run(&streams, &cfgDeps, &pubDeps)
	if err != nil {
		return helper.ExitCodeOf(err), err
	}

	return 0, nil
}

type parseOutcome struct {
	ctx      *kong.Context
	exitCode int
	exited   bool
}

func parseCatchingExit(parser *kong.Kong, args []string) (parseOutcome, error) {
	var (
		outcome  parseOutcome
		parseErr error
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				sig, ok := r.(exitSignal)
				if !ok {
					panic(r)
				}

				outcome.exitCode = sig.code
				outcome.exited = true
			}
		}()

		outcome.ctx, parseErr = parser.Parse(args)
	}()

	if parseErr != nil {
		return outcome, fmt.Errorf("%w", parseErr)
	}

	return outcome, nil
}

func handleParseError(err error, opts *runOptions) (int, error) {
	var parseErr *kong.ParseError
	if !errors.As(err, &parseErr) {
		return helper.ExitFailure, fmt.Errorf("%w: %w", helper.ErrUsage, err)
	}

	if enteredConfig(parseErr.Context) {
		sel := parseErr.Context.Selected()
		if sel != nil && sel.Name == configCmdKey && strings.HasPrefix(parseErr.Error(), "expected one of ") {
			printErr := parseErr.Context.PrintUsage(false)
			if printErr != nil {
				return helper.ExitFailure, fmt.Errorf("print config help: %w", printErr)
			}

			return 0, nil
		}

		val, found := helper.ScanOutputFlag(parseErr.Context.Flags(), opts.Args)
		reportedErr := helper.ReportJSONError(
			opts.Stdout,
			found && val == "json",
			nil,
			fmt.Errorf("%w: %w", helper.ErrUsage, err),
		)

		return helper.ExitCodeOf(reportedErr), reportedErr
	}

	prevStdout := parseErr.Context.Kong.Stdout
	parseErr.Context.Kong.Stdout = opts.Stderr
	_ = parseErr.Context.PrintUsage(false)
	parseErr.Context.Kong.Stdout = prevStdout

	return helper.ExitFailure, fmt.Errorf("%w: %w", helper.ErrUsage, err)
}

func enteredConfig(kctx *kong.Context) bool {
	if kctx == nil {
		return false
	}

	for _, p := range kctx.Path {
		if p.Command != nil && p.Command.Name == configCmdKey {
			return true
		}
	}

	return false
}
