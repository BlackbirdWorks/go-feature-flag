package exporter

// EventSize is the size of an event for the byte limit of an exporter: the length of its JSON form, which ends with
// the line break that separates two events in a file. An event that cannot be formatted counts as one byte.
func EventSize(event ExportableEvent) int {
	raw, err := event.FormatInJSON()
	if err != nil {
		return 1
	}

	return len(raw)
}
