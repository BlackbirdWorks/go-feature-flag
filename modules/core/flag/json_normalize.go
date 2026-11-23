package flag

import "unicode/utf8"

// normalizeJSONValue returns the value as json.Unmarshal into an `any` would have produced it
// after a json.Marshal (numbers are float64, objects map[string]any, arrays []any), without going
// through the encoding. ok is false for a value it does not know how to normalize (structs,
// custom marshalers, NaN/Inf, ...): the caller must then use the encoding to get the same result.
//
// A value nested deeper than maxNormalizeDepth (which is what a cycle looks like) is left to the
// encoding too, that reports the cycle as an error.
func normalizeJSONValue(v any) (res any, ok bool) {
	return normalizeJSONValueDepth(v, 0)
}

func normalizeJSONValueDepth(v any, depth int) (res any, ok bool) {
	if depth > maxNormalizeDepth {
		return nil, false
	}
	switch x := v.(type) {
	case nil:
		return nil, true
	case string:
		if !utf8.ValidString(x) {
			return nil, false // the encoding replaces the invalid sequences by U+FFFD
		}
		return v, true // v, not x: boxing x again would allocate
	case bool:
		return v, true
	case float64:
		if x != x || x > maxJSONFloat || x < -maxJSONFloat {
			return nil, false // NaN and Inf can not be encoded
		}
		return v, true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return integerToFloat(v), true
	case float32:
		// json.Marshal formats a float32 with 32 bits precision, so float64(x) would not be what the
		// encoding produces: let the encoding decide.
		return nil, false
	case map[string]any:
		if x == nil {
			return nil, true // encoded as null
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			if !utf8.ValidString(k) {
				return nil, false
			}
			n, ok := normalizeJSONValueDepth(e, depth+1)
			if !ok {
				return nil, false
			}
			out[k] = n
		}
		return out, true
	case []any:
		if x == nil {
			return nil, true // encoded as null
		}
		out := make([]any, len(x))
		for i, e := range x {
			n, ok := normalizeJSONValueDepth(e, depth+1)
			if !ok {
				return nil, false
			}
			out[i] = n
		}
		return out, true
	default:
		return nil, false
	}
}

const maxJSONFloat = 1.7976931348623157e308

// maxNormalizeDepth is the nesting depth above which normalizeJSONValue gives up.
const maxNormalizeDepth = 100

// integerToFloat is what the encoding then the decoding make of an integer.
func integerToFloat(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int8:
		return float64(x)
	case int16:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case uint:
		return float64(x)
	case uint8:
		return float64(x)
	case uint16:
		return float64(x)
	case uint32:
		return float64(x)
	case uint64:
		return float64(x)
	}
	return 0
}
