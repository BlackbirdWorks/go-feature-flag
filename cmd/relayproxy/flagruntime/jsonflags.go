package flagruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// errTrailingData is returned for a JSON flags file with something after its document.
var errTrailingData = errors.New("trailing data after the JSON document")

// errLoneSurrogate is returned for a \u escape of half a surrogate pair, which the JSON decoder would turn into a
// replacement character without a word.
var errLoneSurrogate = errors.New("unpaired surrogate in a \\u escape")

// exactLimit is the largest integer a float64 holds exactly (2^53).
const exactLimit = 1 << 53

// decodeJSONFlags reads a JSON flags file. The decoder of the legacy loader turns every number into a float64,
// which rounds the integers above 2^53; this one keeps such an integer exactly (as int64, or uint64 above the
// range of int64) and gives every other number the float64 that the legacy loader gave it. The accepted forms
// are those of the legacy loader: a blank file is an error, null and {} are a file without flag.
func decodeJSONFlags(data []byte) (map[string]dto.DTO, error) {
	if hasLoneSurrogate(data) {
		return nil, fmt.Errorf("decoding the flags: %w", errLoneSurrogate)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var raw map[string]dto.DTO

	err := decoder.Decode(&raw)
	if err != nil {
		return nil, fmt.Errorf("decoding the flags: %w", err)
	}

	var extra json.RawMessage

	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return nil, fmt.Errorf("decoding the flags: %w", errTrailingData)
	}

	for name := range raw {
		def := raw[name]

		err = exactDTO(&def)
		if err != nil {
			return nil, fmt.Errorf("decoding the flags: flag %q: %w", name, err)
		}
	}

	return raw, nil
}

// exactDTO replaces the [json.Number] values of a flag by exact numbers.
func exactDTO(def *dto.DTO) error {
	if def.Variations != nil {
		err := exactVariations(*def.Variations)
		if err != nil {
			return err
		}
	}

	if def.Metadata != nil {
		err := exactMetadata(*def.Metadata)
		if err != nil {
			return err
		}
	}

	if def.Scheduled == nil {
		return nil
	}

	for idx := range *def.Scheduled {
		step := &(*def.Scheduled)[idx]

		err := exactStep(&step.InternalFlag)
		if err != nil {
			return err
		}
	}

	return nil
}

func exactStep(step *flagdef.InternalFlag) error {
	if step.Variations != nil {
		err := exactVariations(*step.Variations)
		if err != nil {
			return err
		}
	}

	if step.Metadata != nil {
		return exactMetadata(*step.Metadata)
	}

	return nil
}

func exactVariations(variations map[string]*any) error {
	for _, value := range variations {
		if value == nil {
			continue
		}

		exact, err := exactValue(*value)
		if err != nil {
			return err
		}

		*value = exact
	}

	return nil
}

func exactMetadata(metadata map[string]any) error {
	for key, value := range metadata {
		exact, err := exactValue(value)
		if err != nil {
			return err
		}

		metadata[key] = exact
	}

	return nil
}

// exactValue converts the numbers inside a decoded JSON value.
func exactValue(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		return exactNumber(typed)
	case map[string]any:
		for key, item := range typed {
			exact, err := exactValue(item)
			if err != nil {
				return nil, err
			}

			typed[key] = exact
		}
	case []any:
		for idx, item := range typed {
			exact, err := exactValue(item)
			if err != nil {
				return nil, err
			}

			typed[idx] = exact
		}
	}

	return value, nil
}

func exactNumber(number json.Number) (any, error) {
	integer, err := number.Int64()
	if err == nil && (integer > exactLimit || integer < -exactLimit) {
		return integer, nil
	}

	unsigned, err := strconv.ParseUint(number.String(), 10, 64)
	if err == nil && unsigned > exactLimit {
		return unsigned, nil
	}

	floating, err := number.Float64()
	if err != nil {
		return nil, fmt.Errorf("number %s: %w", number, err)
	}

	return floating, nil
}

const (
	escapeLength   = 6 // \uXXXX
	pairLength     = 2 * escapeLength
	highSurrogateL = 0xD800
	highSurrogateH = 0xDBFF
	lowSurrogateL  = 0xDC00
	lowSurrogateH  = 0xDFFF
)

// hasLoneSurrogate reports whether the text has a \u escape of a high surrogate that no low surrogate escape
// follows, or of a low surrogate that no high surrogate precedes.
func hasLoneSurrogate(data []byte) bool {
	for idx := 0; idx < len(data); idx++ {
		if data[idx] != '\\' || idx+1 >= len(data) {
			continue
		}

		if data[idx+1] != 'u' {
			idx++ // another escape: skip the character it escapes, which may be a backslash

			continue
		}

		skip, lone := escapeAt(data, idx)
		if lone {
			return true
		}

		idx += skip
	}

	return false
}

// escapeAt looks at the \uXXXX escape that starts at idx: it says whether it is half a surrogate pair, and how many
// bytes after the first one belong to it (the whole pair when it is a pair).
func escapeAt(data []byte, idx int) (int, bool) {
	unit, ok := hexUnit(data, idx)
	if !ok {
		return 0, false // the decoder reports a malformed escape
	}

	switch {
	case unit >= highSurrogateL && unit <= highSurrogateH:
		next, paired := hexUnit(data, idx+escapeLength)
		if !paired || next < lowSurrogateL || next > lowSurrogateH {
			return 0, true
		}

		return pairLength - 1, false
	case unit >= lowSurrogateL && unit <= lowSurrogateH:
		return 0, true
	}

	return escapeLength - 1, false
}

// hexUnit reads the \uXXXX escape that starts at idx.
func hexUnit(data []byte, idx int) (int64, bool) {
	if idx+escapeLength > len(data) || data[idx] != '\\' || data[idx+1] != 'u' {
		return 0, false
	}

	unit, err := strconv.ParseUint(string(data[idx+2:idx+escapeLength]), 16, 16)
	if err != nil {
		return 0, false
	}

	return int64(unit), true
}
