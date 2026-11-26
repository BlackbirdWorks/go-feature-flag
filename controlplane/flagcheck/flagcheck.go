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

// Error implements error. It names the flags and the reasons, never a value of the file that is not part of the
// engine's message.
func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for i := range e.Problems {
		parts = append(parts, fmt.Sprintf("flag %s: %v", e.Problems[i].Flag, e.Problems[i].Err))
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
		return fmt.Errorf("%w: %w", ErrInvalid, err)
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
