package testutils

import "text/template"

// ExportableMockEvent is an exportable evaluation event used in tests.
type ExportableMockEvent struct {
	name string
}

// NewExportableMockEvent returns an exportable event with the given name.
func NewExportableMockEvent(name string) ExportableMockEvent {
	return ExportableMockEvent{name: name}
}

// GetUserKey returns the name of the event as its user key.
func (e ExportableMockEvent) GetUserKey() string {
	return e.name
}

// GetKey returns the name of the event as its key.
func (e ExportableMockEvent) GetKey() string {
	return e.name
}

// GetCreationDate returns the creation date of the event as a Unix timestamp.
func (ExportableMockEvent) GetCreationDate() int64 {
	return 0
}

// FormatInCSV returns the event formatted as a CSV line.
func (e ExportableMockEvent) FormatInCSV(_ *template.Template) ([]byte, error) {
	return []byte(e.name), nil
}

// FormatInJSON returns the event formatted as JSON.
func (e ExportableMockEvent) FormatInJSON() ([]byte, error) {
	return []byte(`{"name":"` + e.name + `"}`), nil
}
