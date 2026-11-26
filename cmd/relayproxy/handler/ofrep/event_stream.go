package ofrep

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/blackbirdworks/semaphore/internal"
)

var (
	// ErrEventStreamInactivityDelay is the error of a negative inactivity delay.
	ErrEventStreamInactivityDelay = errors.New("the event stream inactivity delay must not be negative, got")
	// ErrEventStreamBaseURL is the error of a base URL that is not an absolute http(s) URL.
	ErrEventStreamBaseURL = errors.New("the event stream base URL must be an absolute http(s) URL")
	// ErrEventStreamBaseURLQuery is the error of a base URL that has a query or a fragment.
	ErrEventStreamBaseURLQuery = errors.New("the event stream base URL must not contain a query or a fragment")
)

// EventStream is what the bulk evaluation response advertises of the flag-change SSE endpoint (OpenFeature
// ADR-0008). The zero value advertises nothing. The only way to set it is [NewEventStream], which validates it, so a
// value of this type is always valid.
type EventStream struct {
	baseURL            string
	inactivityDelaySec int
}

// NewEventStream returns the event stream advertised at baseURL, the public base URL clients use to reach the relay
// proxy (an absolute http or https URL without query or fragment), with the delay in seconds after which a client
// considers the stream inactive (0 omits it). An empty baseURL returns the zero value: nothing is advertised.
// A negative delay or an invalid base URL is an error. The URL may embed credentials: it is never part of the error.
func NewEventStream(baseURL string, inactivityDelaySec int) (EventStream, error) {
	if inactivityDelaySec < 0 {
		return EventStream{}, fmt.Errorf("%w %d", ErrEventStreamInactivityDelay, inactivityDelaySec)
	}

	if baseURL == "" {
		return EventStream{}, nil
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return EventStream{}, fmt.Errorf("%w: %w", ErrEventStreamBaseURL, internal.RedactURLError(err))
	}

	if (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return EventStream{}, ErrEventStreamBaseURL
	}

	if parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return EventStream{}, ErrEventStreamBaseURLQuery
	}

	return EventStream{baseURL: baseURL, inactivityDelaySec: inactivityDelaySec}, nil
}

// IsEnabled reports whether the eventStreams field is added to the bulk evaluation response.
func (e EventStream) IsEnabled() bool { return e.baseURL != "" }
