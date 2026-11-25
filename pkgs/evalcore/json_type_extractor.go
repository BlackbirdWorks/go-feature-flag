package evalcore

// JSONTypeExtractor returns the JSON type name of the variation value.
func JSONTypeExtractor(variation any) string {
	switch variation.(type) {
	case string:
		return "(string)"
	case float64, int:
		return "(number)"
	case bool:
		return "(bool)"
	case []any:
		return "([]interface{})"
	case map[string]any:
		return "(map[string]interface{})"
	}

	return ""
}
