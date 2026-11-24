package helper

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/pterm/pterm"
	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/modules/core/legacy"
)

type Level = string

const (
	WarnLevel    Level = "WARNING"
	InfoLevel    Level = "INFO"
	ErrorLevel   Level = "ERROR"
	DefaultLevel Level = "DEFAULT"
)

type OutputLine struct {
	Text  string
	Level Level
}
type Output struct {
	Lines []OutputLine
}

func (o *Output) Add(line string, level Level) Output {
	o.Lines = append(o.Lines, OutputLine{Text: line, Level: level})

	return *o
}

func (o *Output) PrintLines(cmd *cobra.Command) {
	for _, line := range o.Lines {
		var outputText string
		writer := cmd.OutOrStdout()

		switch line.Level {
		case InfoLevel:
			outputText = pterm.Info.Sprint(line.Text)
		case WarnLevel:
			outputText = pterm.Warning.Sprint(line.Text)
		case ErrorLevel:
			outputText = pterm.Error.Sprint(line.Text)
			writer = cmd.ErrOrStderr()
		default:
			outputText = pterm.Sprint(line.Text)
		}
		_, err := fmt.Fprintln(writer, outputText)
		if err != nil {
			PrintFatalAndExit(err)
		}
	}
}

// PrintFatalAndExit prints err as an error and exits the process with the code 1.
func PrintFatalAndExit(err error) {
	PrintFatal(&pterm.Error, os.Exit, err)
}

// PrintFatal prints err with printer, then calls exit with the code 1. The printer and the exit
// function are parameters so that the callers that must not stop the process (tests) can pass their own.
func PrintFatal(printer *pterm.PrefixPrinter, exit func(int), err error) {
	printer.Printf("error executing command: %v\n", err)
	exit(1)
}

// LegacyWarner returns the Warner of a CLI command: the use of a legacy name is logged once, through
// the default logger of the process.
func LegacyWarner() *legacy.Warner {
	return legacy.NewWarner(func(kind, message string) {
		slog.Default().Warn(message, slog.String("legacyName", kind))
	})
}
