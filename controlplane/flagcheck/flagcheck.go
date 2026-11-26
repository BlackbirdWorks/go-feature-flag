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
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/internal/cache"
)

// The messages of this package are made of facts that do not depend on the content of the file: the key of a flag,
// the line and column the decoder gives, the kind of value found and the type expected, and the category of the
// engine's validation. They never repeat free text of the decoder or of the engine, which can quote a scalar (an API
// key, a variation value) or a targeting query. The original error is always reachable with errors.Unwrap (the decode
// failure) or FlagProblem.Err (the engine's reason): it may quote the file, so it must not be logged where an API key
// matters.

// ErrInvalid is matched (errors.Is) by every *Error, and by the failure to decode the file.
var ErrInvalid = errors.New("flagcheck: invalid flag file")

// maxKey is the longest flag key, in characters, that a message repeats.
const maxKey = 80

// FlagProblem is one flag that the engine rejects.
type FlagProblem struct {
	// Err is the reason given by the engine, as it is: it may quote the file (a query, a value), so do not log it
	// where an API key matters. Error() of the *Error does not carry that text.
	Err error
	// Flag is the key of the flag.
	Flag string
}

// Message is the text for one problem: "flag <key>: <category>". It is safe to print and to log: the key, and the
// category of the engine's reason, never the text of the engine (see Err).
func (p FlagProblem) Message() string {
	return fmt.Sprintf("flag %s: %s", safeKey(p.Flag), category(p.Err))
}

// Error lists the flags that the engine rejects, sorted by flag key.
type Error struct {
	Problems []FlagProblem
}

// Error implements error. For each flag it gives the key (a name the operator wrote; made single-line and cut) and
// the category of the engine's reason, never the text of the engine.
func (e *Error) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for i := range e.Problems {
		parts = append(parts, e.Problems[i].Message())
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
		return fmt.Errorf("%w: %w", ErrInvalid, &decodeError{err: err, format: strings.ToLower(format)})
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

// safeKey makes a flag key fit for a message: one line, control characters gone, at most maxKey characters.
func safeKey(key string) string {
	text := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}

		return r
	}, key)), " ")

	if runes := []rune(text); len(runes) > maxKey {
		return string(runes[:maxKey-1]) + "…"
	}

	return text
}

// decodeError is a failure to decode the file. Its text has the position and the kind of the failure only; Unwrap
// gives the decoder's error, which can quote the file.
type decodeError struct {
	err    error
	format string
}

func (d *decodeError) Unwrap() error { return d.err }

func (d *decodeError) Error() string {
	return "decoding the flags: " + decodeFacts(d.format, d.err)
}

var (
	yamlLine   = regexp.MustCompile(`^line (\d+):`)
	yamlSyntax = regexp.MustCompile(`yaml: line (\d+):`)
	yamlTypeOf = regexp.MustCompile(`^line (\d+): cannot unmarshal (!!\w+) `)
	goTypeName = regexp.MustCompile(`^[\w.*\[\]]+$`)
	jsonKind   = regexp.MustCompile(`^[a-z]+`)
)

// decodeFacts says where and what kind of failure it is, from the position and the types the decoders report.
func decodeFacts(format string, err error) string {
	switch format {
	case "json":
		return jsonFacts(err)
	case "toml":
		if decodeErr, ok := errors.AsType[*toml.DecodeError](err); ok {
			row, column := decodeErr.Position()

			return fmt.Sprintf("invalid TOML at line %d, column %d", row, column)
		}

		return "invalid TOML"
	default:
		return yamlFacts(err)
	}
}

func jsonFacts(err error) string {
	if syntax, ok := errors.AsType[*json.SyntaxError](err); ok {
		return fmt.Sprintf("invalid JSON at byte %d", syntax.Offset)
	}

	if typed, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
		kind := jsonKind.FindString(typed.Value)
		if kind == "" {
			kind = "value"
		}

		return fmt.Sprintf("invalid JSON at byte %d: a %s where %s is expected (field %s)",
			typed.Offset, kind, typed.Type, safeKey(typed.Field))
	}

	return "invalid JSON"
}

func yamlFacts(err error) string {
	if typed, ok := errors.AsType[*yaml.TypeError](err); ok {
		facts := make([]string, 0, len(typed.Errors))

		for _, msg := range typed.Errors {
			facts = append(facts, yamlTypeFact(msg))
		}

		return strings.Join(facts, "; ")
	}

	if match := yamlSyntax.FindStringSubmatch(err.Error()); match != nil {
		return "invalid YAML at line " + match[1]
	}

	return "invalid YAML"
}

// yamlTypeFact reduces one message of a yaml.TypeError ("line 2: cannot unmarshal !!str `abc...` into dto.DTO") to
// its line, the kind found and the type expected. The part between the kind and the type is the start of the value:
// it is dropped, and the type is taken after the last " into " (the value is cut to a few characters, and the type
// follows it).
func yamlTypeFact(msg string) string {
	match := yamlTypeOf.FindStringSubmatch(msg)
	if match == nil {
		if line := yamlLine.FindStringSubmatch(msg); line != nil {
			return "invalid YAML value at line " + line[1]
		}

		return "invalid YAML value"
	}

	_, goType, found := strings.CutLast(msg, " into ")
	if !found || !goTypeName.MatchString(goType) {
		return fmt.Sprintf("line %s: cannot unmarshal a %s", match[1], match[2])
	}

	return fmt.Sprintf("line %s: cannot unmarshal a %s into %s", match[1], match[2], goType)
}

// categories are the fixed phrases of the validation of the engine. A category is a phrase of this list found in the
// engine's message; nothing else of that message is repeated, because the rest can quote a query or a value.
func categories() []string {
	return []string{
		"validating the flag",
		"no default targeting for the flag",
		"nil value for variation",
		"all variations should have the same type",
		"no variation available",
		"missing default rule",
		"duplicated rule name",
		"invalid bucketing key",
		"impossible to return value",
		"invalid percentages: should not be empty",
		"invalid percentages: should not be equal to 0",
		"invalid percentages: the sum",
		"invalid percentage: variation",
		"invalid percentage",
		"invalid progressive rollout, initial percentage should be lower than end percentage",
		"invalid progressive rollout, initial and end steps are required",
		"invalid progressive rollout, end variation",
		"invalid progressive rollout, initial variation",
		"invalid variation",
		"each targeting should have a query",
		"invalid jsonlogic query",
		"invalid query",
		"parsing the query",
	}
}

// category names what the engine rejects. It walks the chain of wrapped errors, outermost first, and for each layer
// takes the fixed phrase that is the PREFIX of that layer's own message (the longest one, at most one per layer): the
// part after it is the wrapped error or a quoted value, and is never searched. A phrase that repeats the one before it
// (a sentinel wrapped by a layer with the same prefix) is named once. A chain with none of them is "rejected by the
// engine".
func category(err error) string {
	var out []string

	walk(err, func(layer string) {
		phrase := prefixPhrase(layer)
		if phrase != "" && (len(out) == 0 || out[len(out)-1] != phrase) {
			out = append(out, phrase)
		}
	})

	if len(out) == 0 {
		return "rejected by the engine"
	}

	return strings.Join(out, ": ")
}

// walk calls visit with the message of err and then of every error it wraps, depth first, in order.
func walk(err error, visit func(layer string)) {
	if err == nil {
		return
	}

	visit(err.Error())

	switch wrapper := err.(type) { //nolint:errorlint // the chain is walked on purpose, layer by layer
	case interface{ Unwrap() []error }:
		for _, inner := range wrapper.Unwrap() {
			walk(inner, visit)
		}
	case interface{ Unwrap() error }:
		walk(wrapper.Unwrap(), visit)
	}
}

// prefixPhrase returns the longest fixed phrase that the message starts with, followed by the end of the message or by
// a character that is not a letter or a digit, or "".
func prefixPhrase(message string) string {
	best := ""

	for _, phrase := range categories() {
		if len(phrase) <= len(best) || !strings.HasPrefix(message, phrase) {
			continue
		}

		rest := message[len(phrase):]
		if rest != "" {
			if r, _ := utf8.DecodeRuneInString(rest); unicode.IsLetter(r) || unicode.IsDigit(r) {
				continue
			}
		}

		best = phrase
	}

	return best
}
