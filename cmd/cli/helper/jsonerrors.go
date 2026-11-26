package helper

import (
	"errors"
	"fmt"
	"io"

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
	cmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return reportJSON(c.OutOrStdout(), opts, fmt.Errorf("%w: %w", ErrUsage, err))
	})

	wrapTree(cmd, opts)
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
