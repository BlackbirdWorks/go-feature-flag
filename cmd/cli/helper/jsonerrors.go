package helper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ErrUsage marks a failure of the command line itself: a missing or extra argument, a flag that does not parse, flags
// that cannot be combined. It exits with [ExitFailure] and is named "usage" in the JSON error object.
var ErrUsage = errors.New("usage")

// reported marks an error whose JSON document was already written as the result of the command (a validation report,
// a verification report): it gets no second document.
type reported struct{ err error }

func (r *reported) Error() string { return r.err.Error() }

func (r *reported) Unwrap() error { return r.err }

// MarkReported marks err as already written to stdout as the result of the command, so that [JSONErrors] does not
// write an error object for it. It keeps the error and its exit code.
func MarkReported(err error) error {
	if err == nil {
		return nil
	}

	return &reported{err: err}
}

// JSONErrorOptions say when and how a failure is written as JSON.
type JSONErrorOptions struct {
	// Enabled says whether the command line asked for JSON output. It is read when a failure happens.
	Enabled func() bool
	// Details lists the individual problems behind an error; it may be nil.
	Details func(err error) []string
	// RawArgs returns the arguments of the command line as given (without the program name). A flag that does not
	// parse stops Cobra before any flag value is known, so JSON mode is then read from these with [RawOutput], in
	// whatever order the flags came. Nil means os.Args[1:].
	RawArgs func() []string
}

// RawOutput walks raw command-line arguments as pflag does, with the flag definitions of the command, and returns the
// value of the last occurrence of the output flag (--output, or its shorthand), and whether there was one. Only a real
// occurrence counts: a known flag that takes a value consumes it (so the "-otext" of "--config -otext" is a value, not
// an output selection, and so is "--output" as the value of another flag), a boolean flag consumes none, shorthands
// combine as pflag combines them, an unknown flag consumes nothing, and "--" ends the flags. A flag after an unknown
// one still counts. The flag set must have a flag named "output"; without one nothing is named.
func RawOutput(flags *pflag.FlagSet, args []string) (string, bool) {
	var (
		last  string
		found bool
	)

	if flags == nil || flags.Lookup(outputFlag) == nil {
		return "", false
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			return last, found
		case strings.HasPrefix(arg, "--"):
			used, value, isOutput := longFlag(flags, strings.TrimPrefix(arg, "--"), args[i+1:])
			i += used

			if isOutput {
				last, found = value, true
			}
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			consumed, value, isOutput := shorthandRun(flags, arg[1:], args[i+1:])
			i += consumed

			if isOutput {
				last, found = value, true
			}
		}
	}

	return last, found
}

// longFlag reads one long flag ("name" or "name=value", without the dashes) the way pflag does. It returns how many of
// the following arguments it used as a value, the value of the output flag, and whether it was the output flag.
func longFlag(flags *pflag.FlagSet, arg string, following []string) (int, string, bool) {
	name, value, hasValue := strings.Cut(arg, "=")

	flag := flags.Lookup(name)
	if flag == nil {
		return 0, "", false // unknown: pflag stops with an error, nothing is consumed
	}

	used := 0

	if !hasValue && flag.NoOptDefVal == "" {
		if len(following) == 0 {
			return 0, "", false // a flag without its value: pflag fails, nothing is selected
		}

		value = following[0]
		used = 1
	}

	return used, value, name == outputFlag
}

// outputFlag is the name of the output flag of the commands.
const outputFlag = "output"

// shorthandRun reads a run of shorthand flags ("vo", "ojson", "o=json") the way pflag does. It returns how many of the
// following arguments it used as a value, the value of the output flag when the run ended with it, and whether it did.
func shorthandRun(flags *pflag.FlagSet, run string, following []string) (int, string, bool) {
	output := flags.Lookup(outputFlag)

	for run != "" {
		flag := flags.ShorthandLookup(run[:1])
		if flag == nil {
			return 0, "", false // unknown: pflag stops with an error, nothing is consumed
		}

		if flag.NoOptDefVal != "" {
			run = run[1:] // a boolean flag takes no value

			continue
		}

		rest := strings.TrimPrefix(run[1:], "=")
		used := 0

		if len(run) == 1 {
			if len(following) == 0 {
				return 0, "", false // a flag without its value: pflag fails, nothing is selected
			}

			rest = following[0]
			used = 1
		}

		return used, rest, flag == output
	}

	return 0, "", false
}

// JSONErrors makes every failure of cmd and of all its subcommands, present and future, follow one mechanism:
//   - the error always carries an exit code ([ExitError], [ExitFailure] when it has none);
//   - an argument-count error and a flag-parse error are usage errors ([ErrUsage]);
//   - when opts.Enabled() is true, one JSON error object is written to stdout (see [WriteJSONError]), unless the error
//     was marked with [MarkReported].
//
// Call it once, on the command that owns the output flag, after its subcommands were added. A command with a
// --output json flag cannot forget it: the wrap is applied to the whole tree.
func JSONErrors(cmd *cobra.Command, opts JSONErrorOptions) {
	cmd.SetFlagErrorFunc(func(failed *cobra.Command, err error) error {
		// The flags were not parsed to the end: the flag value that was read so far is not reliable.
		raw := opts
		raw.Enabled = func() bool {
			// A flag the command line names decides, last one wins, even when it says text. The parsed value is only
			// used when the arguments name no output flag.
			if value, named := RawOutput(failed.Flags(), rawArgs(opts)); named {
				return value == "json"
			}

			return opts.Enabled()
		}

		return reportJSON(failed.OutOrStdout(), raw, fmt.Errorf("%w: %w", ErrUsage, err))
	})

	wrapTree(cmd, opts)
}

func rawArgs(opts JSONErrorOptions) []string {
	if opts.RawArgs != nil {
		return opts.RawArgs()
	}

	return os.Args[1:]
}

func wrapTree(cmd *cobra.Command, opts JSONErrorOptions) {
	if validate := cmd.Args; validate != nil {
		cmd.Args = func(self *cobra.Command, args []string) error {
			err := validate(self, args)
			if err == nil {
				return nil
			}

			return reportJSON(self.OutOrStdout(), opts, fmt.Errorf("%w: %w", ErrUsage, err))
		}
	}

	if run := cmd.RunE; run != nil {
		cmd.RunE = func(c *cobra.Command, args []string) error {
			return reportJSON(c.OutOrStdout(), opts, run(c, args))
		}
	}

	for _, sub := range cmd.Commands() {
		wrapTree(sub, opts)
	}
}

func reportJSON(stdout io.Writer, opts JSONErrorOptions, err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*ExitError](err); !ok {
		err = &ExitError{Err: err, Code: ExitFailure}
	}

	_, done := errors.AsType[*reported](err)
	if !opts.Enabled() || done {
		return err
	}

	name := JSONErrorName(ExitCodeOf(err))
	if errors.Is(err, ErrUsage) {
		name = "usage"
	}

	var details []string
	if opts.Details != nil {
		details = opts.Details(err)
	}

	writeErr := WriteJSONError(stdout, name, err, details)
	if writeErr != nil {
		return fmt.Errorf("report error: %w", writeErr)
	}

	return err
}
