package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The values of --output.
const (
	outputText = "text"
	outputJSON = "json"
)

// ErrUsage is returned for flags that are missing or have a value that is not allowed.
var ErrUsage = errors.New("usage")

// Deps are the parts of the outside world that the commands use.
type Deps struct {
	// LookupEnv reads an environment variable: the SEMAPHORE_* overrides apply to the effective configuration.
	LookupEnv func(string) (string, bool)
	// ReadFile reads the bootstrap file.
	ReadFile func(string) ([]byte, error)
}

// DefaultDeps returns the dependencies of the real command.
func DefaultDeps() Deps { return Deps{LookupEnv: os.LookupEnv, ReadFile: os.ReadFile} }

// NewConfigCmd returns the config command group, wired to the real environment.
func NewConfigCmd() *cobra.Command { return NewConfigCmdWith(DefaultDeps()) }

// NewConfigCmdWith returns the config command group with the given dependencies.
func NewConfigCmdWith(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Validate and print the version 2 bootstrap file of the relay proxy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	cmd.AddCommand(newValidateCmd(&deps), newPrintCmd(&deps))

	return cmd
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
		return fmt.Errorf("%w: --output must be text or json, not %q", ErrUsage, format)
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

func newValidateCmd(deps *Deps) *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "validate <file>",
		Short: "Check a bootstrap file with the loader of the relay proxy",
		Long: `Reads <file> with the strict loader of the relay proxy: version 2 required, unknown keys, bad references and
bad values are errors that name the file, the path of the key and the line. Secret references are checked for their
shape, not resolved. Exit code 0 when valid, 2 when invalid, 1 on another failure.`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runValidate(cmd.OutOrStdout(), args[0], output, deps)
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", outputText, "output format: text or json")

	return cmd
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

func newPrintCmd(deps *Deps) *cobra.Command {
	var (
		output    string
		effective bool
	)

	cmd := &cobra.Command{
		Use:   "print --effective <file>",
		Short: "Print the effective bootstrap file: defaults and environment overrides applied, secrets redacted",
		Long: `Prints what the relay proxy would run with: the file after the SEMAPHORE_* environment overrides and the
defaults. Secrets, API keys and admin keys print as [REDACTED]; references (fromEnv, fromFile) print as written.
--effective is required. Exit code 2 when the file is invalid.`,
		Args:          cobra.ExactArgs(1),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !effective {
				return fmt.Errorf("%w: --effective is required", ErrUsage)
			}

			return runPrint(cmd.OutOrStdout(), args[0], output, deps)
		},
	}
	cmd.Flags().BoolVar(&effective, "effective", false, "print the effective configuration (required)")
	cmd.Flags().StringVarP(&output, "output", "o", outputText, "output format: text (YAML) or json")

	return cmd
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
