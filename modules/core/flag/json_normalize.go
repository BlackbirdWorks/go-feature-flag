package flag

// normalizeJSONValue returns the value as json.Unmarshal into an `any` would have produced it
// after a json.Marshal (numbers are float64, objects map[string]any, arrays []any), without going
// through the encoding. ok is false for a value it does not know how to normalize (structs,
// custom marshalers, NaN/Inf, ...): the caller must then use the encoding to get the same result.
func normalizeJSONValue(v any) (res any, ok bool) {
	switch x := v.(type) {
	case nil:
		return nil, true
	case string, bool, float64:
		if f, isFloat := x.(float64); isFloat && (f != f || f > maxJSONFloat || f < -maxJSONFloat) {
			return nil, false // NaN and Inf can not be encoded
		}
		return x, true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		// json.Marshal formats a float32 with 32 bits precision, so float64(x) would not be what the
		// encoding produces: let the encoding decide.
		return nil, false
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			n, ok := normalizeJSONValue(e)
			if !ok {
				return nil, false
			}
			out[k] = n
		}
		return out, true
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			n, ok := normalizeJSONValue(e)
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
