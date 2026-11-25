package ffcontext

import "time"

// GoffContextSpecifics holds the context fields specific to GO Feature Flag.
type GoffContextSpecifics struct {
	// CurrentDateTime is the current date time to use for the evaluation.
	CurrentDateTime *time.Time `json:"currentDateTime"`
	// ExporterMetadata is the metadata to be used by the exporter.
	ExporterMetadata map[string]any `json:"exporterMetadata"`
	// FlagList is the list of flags to evaluate in a bulk evaluation.
	FlagList []string `json:"flagList"`
	// UsedLegacyKey is true when the context carried these fields under the legacy attribute name
	// (LegacyReservedKey). The code that evaluates the flags reports it, once, through its own
	// legacy.Warner: extracting the fields has no owner to warn.
	UsedLegacyKey bool `json:"-"`
}

// addCurrentDateTime adds the current date time to the context.
// This function formats the current date time to RFC3339 format.
func (g *GoffContextSpecifics) addCurrentDateTime(currentDateTime any) {
	switch value := currentDateTime.(type) {
	case *time.Time:
		g.CurrentDateTime = value
	case time.Time:
		g.CurrentDateTime = &value
	case string:
		parsedTime, err := time.Parse(time.RFC3339, value)
		if err == nil {
			g.CurrentDateTime = &parsedTime
		}

		return
	default:
		return
	}
}

// addListFlags adds the list of flags to evaluate in a bulk evaluation.
func (g *GoffContextSpecifics) addListFlags(flagList any) {
	if value, ok := flagList.([]string); ok {
		g.FlagList = value
	}

	if value, ok := flagList.([]any); ok {
		// an explicit empty list is kept as an empty (not nil) list: it is set, an empty list means all the flags.
		g.FlagList = make([]string, 0, len(value))
		for _, val := range value {
			if valAsString, isString := val.(string); isString {
				g.FlagList = append(g.FlagList, valAsString)
			}
		}
	}
}

func (g *GoffContextSpecifics) addExporterMetadata(exporterMetadata any) {
	if value, ok := exporterMetadata.(map[string]any); ok {
		g.ExporterMetadata = value
	}
}
