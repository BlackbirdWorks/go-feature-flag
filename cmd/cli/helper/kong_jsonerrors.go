package helper

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/kong"
)

const outputShort = 'o'

// ReportJSONError ensures err carries an [ExitError] (defaulting to [ExitFailure]) and, when enabled is true and err
// has not been marked with [MarkReported], writes one JSON error object to stdout before returning err.
func ReportJSONError(stdout io.Writer, enabled bool, details func(error) []string, err error) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*ExitError](err); !ok {
		err = &ExitError{Err: err, Code: ExitFailure}
	}

	_, done := errors.AsType[*reported](err)
	if !enabled || done {
		return err
	}

	name := JSONErrorName(ExitCodeOf(err))
	if errors.Is(err, ErrUsage) {
		name = "usage"
	}

	var detailList []string
	if details != nil {
		detailList = details(err)
	}

	writeErr := WriteJSONError(stdout, name, err, detailList)
	if writeErr != nil {
		return fmt.Errorf("report error: %w", writeErr)
	}

	return err
}

// ScanOutputFlag scans raw command-line arguments using Kong's flag metadata to find the last value given to --output
// or -o, even when an unknown or malformed flag stops Kong's parser before --output is reached. Unknown flags and
// malformed tokens are skipped, "--" stops scanning, and values belonging to known non-boolean flags are consumed so
// flag-like values (such as "--config -otext") are not misread.
func ScanOutputFlag(flags []*kong.Flag, args []string) (string, bool) {
	longFlags, shortFlags, hasOutput := indexFlags(flags)
	if !hasOutput {
		return "", false
	}

	var state outputScan

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}

		next := ""
		hasNext := i+1 < len(args)

		if hasNext {
			next = args[i+1]
		}

		var consumedNext bool

		switch {
		case strings.HasPrefix(arg, "--") && len(arg) > 2:
			consumedNext = scanLongFlag(arg[2:], next, hasNext, longFlags, &state)
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			consumedNext = scanShortFlags(arg[1:], next, hasNext, shortFlags, &state)
		}

		if consumedNext {
			i++
		}
	}

	return state.last, state.set
}

type outputScan struct {
	last string
	set  bool
}

func indexFlags(flags []*kong.Flag) (map[string]bool, map[rune]bool, bool) {
	longFlags := make(map[string]bool, len(flags))
	shortFlags := make(map[rune]bool, len(flags))
	hasOutput := false

	for _, flag := range flags {
		if flag == nil || flag.Value == nil {
			continue
		}

		isBool := flag.IsBool() || flag.IsCounter()
		longFlags[flag.Name] = isBool

		if flag.Short != 0 {
			shortFlags[flag.Short] = isBool
		}

		if flag.Name == outputFlag || flag.Short == outputShort {
			hasOutput = true
		}
	}

	return longFlags, shortFlags, hasOutput
}

func scanLongFlag(rest, next string, hasNext bool, longFlags map[string]bool, state *outputScan) bool {
	name, val, hasEq := strings.Cut(rest, "=")

	isBool, known := longFlags[name]
	if !known {
		return false
	}

	if name == outputFlag {
		if hasEq {
			state.last, state.set = val, true

			return false
		}

		if hasNext {
			state.last, state.set = next, true

			return true
		}

		return false
	}

	return !isBool && !hasEq && hasNext
}

func scanShortFlags(shorts, next string, hasNext bool, shortFlags map[rune]bool, state *outputScan) bool {
	for i := 0; i < len(shorts); {
		r, size := utf8.DecodeRuneInString(shorts[i:])
		i += size

		isBool, known := shortFlags[r]
		if !known {
			return false
		}

		tail := shorts[i:]

		if r == outputShort {
			if after, ok := strings.CutPrefix(tail, "="); ok {
				state.last, state.set = after, true

				return false
			}

			if tail != "" {
				state.last, state.set = tail, true

				return false
			}

			if hasNext {
				state.last, state.set = next, true

				return true
			}

			return false
		}

		if isBool {
			if strings.HasPrefix(tail, "=") {
				return false
			}

			continue
		}

		return tail == "" && hasNext
	}

	return false
}
