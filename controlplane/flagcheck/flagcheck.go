// Package flagcheck validates the bytes of a flag object with the engine of the relay: the same decoder and the
// same validity checks the relay applies when it loads a flag file, and that "semaphore-cli lint" applies.
//
// The check is what the relay would do, so a flag that passes here is not dropped at load time. Besides the
// structural rules of controlplane.ValidateFlags it compiles the targeting queries, checks the percentages,
// the variations and the rollouts, as the engine does.
//
// The package imports the engine, which is heavy. It is separate from controlplane and from s3publish on
// purpose: a publisher that only needs the document format does not pull it in.
package flagcheck

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/internal/cache"
)

// ErrInvalid is matched (errors.Is) by every *Error, and by the failure to decode the file.
var ErrInvalid = errors.New("flagcheck: invalid flag file")

// FlagProblem is one flag that the engine rejects.
type FlagProblem struct {
	// Err is the reason given by the engine.
	Err error
	// Flag is the key of the flag.
	Flag string
}

// Error lists the flags that the engine rejects, sorted by flag key.
type Error struct {
	Problems []FlagProblem
}

// quoted matches the pieces of an engine message that repeat the content of the file: a scalar quoted in backticks
// (the decoder shows the start of the value it could not read) and the text of a targeting query, as in
// `invalid rule "..."`.
var quoted = regexp.MustCompile("`[^`]*`|invalid rule \"(?:[^\"\\\\]|\\\\.)*\"")

// redact removes from a message of the engine what quotes the content of the file, and keeps where and why it failed.
func redact(msg string) string {
	return quoted.ReplaceAllStringFunc(msg, func(match string) string {
		if strings.HasPrefix(match, "invalid rule") {
			return "invalid rule"
		}

		return "`...`"
	})
}

// redacted is an error whose text does not quote the file; it still unwraps to the engine's error.
type redacted struct{ err error }

func (r redacted) Error() string { return redact(r.err.Error()) }
func (r redacted) Unwrap() error { return r.err }

// Error implements error. It names the flags and the reasons. The text of the engine is kept, except that what
// quotes the content of the file (a scalar it could not read, the text of a targeting query) is removed, so the
// message carries no value of the file. Problems[i].Err is the engine's error as it is.
func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for i := range e.Problems {
		parts = append(parts, fmt.Sprintf("flag %s: %s", e.Problems[i].Flag, redact(e.Problems[i].Err.Error())))
	}

	return strings.Join(parts, "; ")
}

// Is reports a match with ErrInvalid.
func (*Error) Is(target error) bool { return target == ErrInvalid }

// Check decodes a flag file in the given format ("yaml", "json" or "toml") with the loader of the relay and
// validates every flag. It returns nil, an error that matches ErrInvalid and wraps the decoding failure, or an
// *Error.
func Check(body []byte, format string) error {
	flags, err := cache.ConvertToFlagStruct(body, format)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, redacted{err: err})
	}

	flagErrors := configfile.ValidateFlags(flags)
	if len(flagErrors) == 0 {
		return nil
	}

	out := &Error{Problems: make([]FlagProblem, 0, len(flagErrors))}
	for i := range flagErrors {
		out.Problems = append(out.Problems, FlagProblem{Flag: flagErrors[i].Flag, Err: flagErrors[i].Err})
	}

	return out
}
