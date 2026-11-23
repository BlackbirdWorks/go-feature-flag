package relay

import (
	"fmt"
	"net/http"
)

// Endpoint is one of the relay proxy evaluation endpoints.
type Endpoint string

const (
	// GOFFEval is POST /v1/feature/{key}/eval.
	GOFFEval Endpoint = "goff-eval"
	// GOFFAllFlags is POST /v1/allflags.
	GOFFAllFlags Endpoint = "goff-allflags"
	// OFREPEval is POST /ofrep/v1/evaluate/flags/{key}.
	OFREPEval Endpoint = "ofrep-eval"
	// OFREPBulk is POST /ofrep/v1/evaluate/flags.
	OFREPBulk Endpoint = "ofrep-bulk"
)

// Endpoints lists all the endpoints.
var Endpoints = []Endpoint{GOFFEval, GOFFAllFlags, OFREPEval, OFREPBulk}

// Path returns the URL path of the endpoint for a flag key.
func (e Endpoint) Path(flagKey string) string {
	switch e {
	case GOFFEval:
		return "/v1/feature/" + flagKey + "/eval"
	case GOFFAllFlags:
		return "/v1/allflags"
	case OFREPEval:
		return "/ofrep/v1/evaluate/flags/" + flagKey
	default:
		return "/ofrep/v1/evaluate/flags"
	}
}

// Body returns a request body with a realistic evaluation context (10 attributes) for the user i.
func (e Endpoint) Body(i int) string {
	user := i % 1000
	custom := fmt.Sprintf(`"email":"user-%d@example.com","country":"FR","age":34,"plan":"pro",`+
		`"region":"eu-west-1","beta":true,"version":"1.42.0","device":"ios","company":"acme"`, user)
	switch e {
	case GOFFEval:
		return fmt.Sprintf(`{"evaluationContext":{"key":"user-%d","custom":{%s}},"defaultValue":false}`, user, custom)
	case GOFFAllFlags:
		return fmt.Sprintf(`{"evaluationContext":{"key":"user-%d","custom":{%s}}}`, user, custom)
	default:
		return fmt.Sprintf(`{"context":{"targetingKey":"user-%d",%s}}`, user, custom)
	}
}

// NewRequest builds the HTTP request (without a URL host) for the endpoint.
func (e Endpoint) NewRequest(baseURL, flagKey string, i int) (*http.Request, error) {
	return newJSONRequest(baseURL+e.Path(flagKey), e.Body(i))
}
