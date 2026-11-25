package relay

import (
	"errors"
	"fmt"
	"net/http"
)

const (
	// distinctUsers is the number of distinct users of the generated evaluation contexts.
	distinctUsers = 1000
)

var (
	errUnknownEndpoint = errors.New("unknown endpoint")
)

// Endpoint is one of the relay proxy evaluation endpoints.
type Endpoint string

const (
	// SemaphoreEval is POST /v1/feature/{key}/eval.
	SemaphoreEval Endpoint = "semaphore-eval"
	// SemaphoreAllFlags is POST /v1/allflags.
	SemaphoreAllFlags Endpoint = "semaphore-allflags"
	// OFREPEval is POST /ofrep/v1/evaluate/flags/{key}.
	OFREPEval Endpoint = "ofrep-eval"
	// OFREPBulk is POST /ofrep/v1/evaluate/flags.
	OFREPBulk Endpoint = "ofrep-bulk"
)

// Endpoints returns all the endpoints.
func Endpoints() []Endpoint {
	return []Endpoint{SemaphoreEval, SemaphoreAllFlags, OFREPEval, OFREPBulk}
}

// ParseEndpoint validates an endpoint name. The legacy goff-eval and goff-allflags names are aliases.
func ParseEndpoint(name string) (Endpoint, error) {
	// Legacy spellings from before the rename are still accepted.
	switch name {
	case "goff-eval":
		name = string(SemaphoreEval)
	case "goff-allflags":
		name = string(SemaphoreAllFlags)
	}

	for _, e := range Endpoints() {
		if string(e) == name {
			return e, nil
		}
	}

	return "", fmt.Errorf("%w %q (want one of %v)", errUnknownEndpoint, name, Endpoints())
}

// EmitsEvents reports whether evaluating through the endpoint queues feature events in the
// exporter. The bulk endpoints (GetFlagStates / AllFlagsState) evaluate without emitting events.
func (e Endpoint) EmitsEvents() bool {
	return e == SemaphoreEval || e == OFREPEval
}

// Path returns the URL path of the endpoint for a flag key.
func (e Endpoint) Path(flagKey string) string {
	switch e {
	case SemaphoreEval:
		return "/v1/feature/" + flagKey + "/eval"
	case SemaphoreAllFlags:
		return "/v1/allflags"
	case OFREPEval:
		return "/ofrep/v1/evaluate/flags/" + flagKey
	case OFREPBulk:
		return "/ofrep/v1/evaluate/flags"
	default:
		return ""
	}
}

// Body returns a request body with a realistic evaluation context (10 attributes) for the user i.
func (e Endpoint) Body(i int) string {
	user := i % distinctUsers
	custom := fmt.Sprintf(`"email":"user-%d@example.com","country":"FR","age":34,"plan":"pro",`+
		`"region":"eu-west-1","beta":true,"version":"1.42.0","device":"ios","company":"acme"`, user)

	switch e {
	case SemaphoreEval:
		return fmt.Sprintf(`{"evaluationContext":{"key":"user-%d","custom":{%s}},"defaultValue":false}`, user, custom)
	case SemaphoreAllFlags:
		return fmt.Sprintf(`{"evaluationContext":{"key":"user-%d","custom":{%s}}}`, user, custom)
	case OFREPEval, OFREPBulk:
		return fmt.Sprintf(`{"context":{"targetingKey":"user-%d",%s}}`, user, custom)
	default:
		return fmt.Sprintf(`{"context":{"targetingKey":"user-%d",%s}}`, user, custom)
	}
}

// NewRequest builds the HTTP request (without a URL host) for the endpoint.
func (e Endpoint) NewRequest(baseURL, flagKey string, i int) (*http.Request, error) {
	if e.Path(flagKey) == "" {
		return nil, fmt.Errorf("%w %q", errUnknownEndpoint, string(e))
	}

	return newJSONRequest(baseURL+e.Path(flagKey), e.Body(i))
}
