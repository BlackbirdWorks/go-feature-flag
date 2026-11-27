package helper

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/modules/core/legacy"
)

// Level is the severity of a line of command output.
type Level = string

const (
	// WarnLevel is the severity of a warning line of command output.
	WarnLevel Level = "WARNING"
	// InfoLevel is the severity of an informational diagnostic (progress, notes): it goes to stderr.
	InfoLevel Level = "INFO"
	// StatusLevel is the severity of a line that is the result of the command (for example "Valid Semaphore
	// configuration"): it is printed like an info line, on stdout.
	StatusLevel Level = "STATUS"
	// ErrorLevel is the severity of an error line of command output.
	ErrorLevel Level = "ERROR"
	// DefaultLevel is the severity of an output line when none is given.
	DefaultLevel Level = "DEFAULT"
)

// OutputLine is a line of command output with its severity.
type OutputLine struct {
	Text  string
	Level Level
}

// Output collects the lines printed by a command.
type Output struct {
	Lines []OutputLine
}

// Add appends a line with the given level to the output and returns it.
func (o *Output) Add(line string, level Level) Output {
	o.Lines = append(o.Lines, OutputLine{Text: line, Level: level})

	return *o
}

// PrintLines writes the collected lines. stdout carries results only: [StatusLevel] and [DefaultLevel] lines go to
// stdout; every diagnostic ([InfoLevel], [WarnLevel], [ErrorLevel]) goes to stderr.
func (o *Output) PrintLines(cmd *cobra.Command) {
	o.PrintLinesTo(cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// PrintLinesTo writes the collected lines to stdout and stderr. stdout carries results only: [StatusLevel] and
// [DefaultLevel] lines go to stdout; every diagnostic ([InfoLevel], [WarnLevel], [ErrorLevel]) goes to stderr.
func (o *Output) PrintLinesTo(stdout, stderr io.Writer) {
	for _, line := range o.Lines {
		var outputText string

		writer := stdout

		switch line.Level {
		case StatusLevel:
			outputText = pterm.Info.Sprint(line.Text)
		case InfoLevel:
			outputText = pterm.Info.Sprint(line.Text)
			writer = stderr
		case WarnLevel:
			outputText = pterm.Warning.Sprint(line.Text)
			writer = stderr
		case ErrorLevel:
			outputText = pterm.Error.Sprint(line.Text)
			writer = stderr
		default:
			outputText = pterm.Sprint(line.Text)
		}

		_, err := fmt.Fprintln(writer, outputText)
		if err != nil {
			PrintFatalAndExit(err)
		}
	}
}

// PrintFatalAndExit prints err as an error on stderr and exits the process with the code of the error
// ([ExitCodeOf]), 1 for an error that asks for none.
func PrintFatalAndExit(err error) {
	PrintFatal(FatalPrinter(os.Stderr), os.Exit, err)
}

// FatalPrinter returns the printer of the final error line of every command: it writes to stderr, so that stdout
// carries results only.
func FatalPrinter(stderr io.Writer) *pterm.PrefixPrinter {
	return pterm.Error.WithWriter(stderr)
}

// PrintFatal prints err with printer, then calls exit with the code of the error ([ExitCodeOf]). The printer and
// the exit function are parameters so that the callers that must not stop the process (tests) can pass their own.
func PrintFatal(printer *pterm.PrefixPrinter, exit func(int), err error) {
	printer.Printf("error executing command: %v\n", err)
	exit(ExitCodeOf(err))
}

// LegacyWarner returns the Warner of a CLI command: the use of a legacy name is logged once, through
// the default logger of the process.
func LegacyWarner() *legacy.Warner {
	return LegacyWarnerTo(slog.Default)
}

// LegacyWarnerTo is LegacyWarner with the logger it writes to: logger is called for every warning, so a
// default logger replaced after the warner was built is still honoured.
func LegacyWarnerTo(logger func() *slog.Logger) *legacy.Warner {
	return legacy.NewWarner(func(kind, message string) {
		logger().Warn("legacy name used", slog.String("legacyName", kind), slog.String("detail", message))
	})
}
