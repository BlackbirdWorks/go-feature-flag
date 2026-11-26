package helper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
	// parse stops Cobra before any flag value is known, so JSON mode is then read from these with [WantsJSON], in
	// whatever order the flags came. Nil means os.Args[1:].
	RawArgs func() []string
}

// WantsJSON reports whether raw command-line arguments ask for --output json: the last of -o VALUE, -oVALUE,
// -o=VALUE, --output VALUE and --output=VALUE decides, and the scan stops at "--". It is a pure scan: it does not
// know the other flags, so a value that merely equals "json" for another flag does not count.
func WantsJSON(args []string) bool {
	value, _ := RawOutput(args)

	return value == "json"
}

// RawOutput returns the value of the last output flag in raw command-line arguments, and whether there was one.
// The rules are those of [WantsJSON].
func RawOutput(args []string) (string, bool) {
	var (
		last  string
		found bool
	)

	for i := 0; i < len(args); i++ {
		arg := args[i]

		var value string

		switch {
		case arg == "--":
			return last, found
		case arg == "-o" || arg == "--output":
			if i+1 >= len(args) {
				continue
			}

			i++
			value = args[i]
		case strings.HasPrefix(arg, "--output="):
			value = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			value = strings.TrimPrefix(arg, "-o=")
		case strings.HasPrefix(arg, "-o") && !strings.HasPrefix(arg, "--"):
			value = strings.TrimPrefix(arg, "-o")
		default:
			continue
		}

		last, found = value, true
	}

	return last, found
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
			if value, named := RawOutput(rawArgs(opts)); named {
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
