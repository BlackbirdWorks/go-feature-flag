package relay

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
)

const (
	// clientTimeout is the timeout of the requests, connsPerWorker the idle connections kept per worker.
	clientTimeout  = 30 * time.Second
	connsPerWorker = 2
)

var errChooseFlagWithTracking = errors.New("choose a flag with tracking on")

var (
	errUnexpectedStatus       = errors.New("unexpected status")
	errEndpoint               = errors.New("endpoint")
	errEvaluationFailed       = errors.New("evaluation failed")
	errResponseProtocolIsHTTP = errors.New("response protocol is HTTP/")
)

// Target evaluates flags against a real relay proxy over loopback HTTP.
type Target struct {
	relay    *Relay
	client   *http.Client
	endpoint Endpoint
	flagKey  string
	h2c      bool
	perCall  int
}

// NewTarget starts a relay proxy and returns a soak target for the endpoint.
// It rejects an unknown endpoint, and a slow or failing exporter on an endpoint that does not
// emit events (the exporter would be bypassed and the soak would not measure what it claims).
func NewTarget(o Options, endpoint Endpoint, flagKey string, workers int) (*Target, error) {
	if err := validateTarget(o, endpoint, flagKey); err != nil {
		return nil, err
	}
	o.Listen = true
	r, err := Start(o)
	if err != nil {
		return nil, err
	}
	t := &Target{
		relay:    r,
		endpoint: endpoint,
		flagKey:  flagKey,
		h2c:      o.H2C,
		perCall:  1,
		client: &http.Client{
			Timeout: clientTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        workers * connsPerWorker,
				MaxIdleConnsPerHost: workers * connsPerWorker,
				IdleConnTimeout:     time.Minute,
				Protocols:           clientProtocols(o.H2C),
			},
		},
	}
	if !endpoint.EmitsEvents() {
		if t.perCall, err = t.countFlagsPerCall(); err != nil {
			t.Close()

			return nil, err
		}
	}

	return t, nil
}

// clientProtocols is HTTP/1.1 with keep-alive, or HTTP/2 over cleartext with prior knowledge (no HTTP/1.1
// fallback, so a server that does not speak h2c is an error and not a silent downgrade).
func clientProtocols(h2c bool) *http.Protocols {
	var p http.Protocols
	if h2c {
		p.SetUnencryptedHTTP2(true)
	} else {
		p.SetHTTP1(true)
	}

	return &p
}

// countFlagsPerCall sends one request and counts the flags of the bulk response.
func (t *Target) countFlagsPerCall() (int, error) {
	req, err := t.endpoint.NewRequest(t.relay.BaseURL, t.flagKey, 0)
	if err != nil {
		return 0, err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%w %d", errUnexpectedStatus, resp.StatusCode)
	}

	return countFlags(body)
}

// countFlags returns the number of flags of a bulk response: the "flags" member is an object keyed by flag
// (all flags endpoint) or an array (OFREP bulk).
func countFlags(body []byte) (int, error) {
	var v struct {
		Flags json.RawMessage `json:"flags"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return 0, fmt.Errorf("invalid bulk response: %w", err)
	}
	var asArray []json.RawMessage
	if err := json.Unmarshal(v.Flags, &asArray); err == nil {
		return max(1, len(asArray)), nil
	}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(v.Flags, &asMap); err != nil {
		return 0, fmt.Errorf("bulk response without flags: %w", err)
	}

	return max(1, len(asMap)), nil
}

// FlagsPerCall implements loadtest.Weighted: the number of flags of a bulk response, 1 for a single evaluation.
func (t *Target) FlagsPerCall() int { return t.perCall }

// Protocol returns the HTTP protocol the target speaks ("HTTP/1.1" or "h2c").
func (t *Target) Protocol() string {
	if t.h2c {
		return "h2c"
	}

	return "HTTP/1.1"
}

func validateTarget(o Options, endpoint Endpoint, flagKey string) error {
	if _, err := ParseEndpoint(string(endpoint)); err != nil {
		return err
	}
	if o.ExporterMode != "" {
		if _, err := loadtest.ParseExporterMode(string(o.ExporterMode)); err != nil {
			return err
		}
	}
	if !endpoint.EmitsEvents() &&
		(o.ExporterMode == loadtest.ExporterSlow || o.ExporterMode == loadtest.ExporterFailing) {
		return fmt.Errorf(
			"%w %q does not emit events, so exporter %q would never be exercised: use %s or %s",
			errEndpoint,
			endpoint,
			o.ExporterMode,
			SemaphoreEval,
			OFREPEval,
		)
	}
	if endpoint.EmitsEvents() &&
		(o.ExporterMode == loadtest.ExporterSlow || o.ExporterMode == loadtest.ExporterFailing) &&
		!tracksEvents(o.FlagsYAML, flagKey) {
		return fmt.Errorf("flag %q has trackEvents disabled, so exporter %q would never be exercised: %w",
			flagKey, o.ExporterMode, errChooseFlagWithTracking)
	}

	return nil
}

// tracksEvents reports whether the flag emits events: trackEvents defaults to true. A flag that
// is not in the configuration (or a configuration that cannot be parsed) is not rejected here,
// the evaluation reports it.
func tracksEvents(flagsYAML, flagKey string) bool {
	var flags map[string]struct {
		TrackEvents *bool `yaml:"trackEvents"`
	}
	if err := yaml.Unmarshal([]byte(flagsYAML), &flags); err != nil {
		return true
	}
	f, ok := flags[flagKey]

	return !ok || f.TrackEvents == nil || *f.TrackEvents
}

// checkResponse returns an error when a 200 response of the endpoint reports a failed evaluation.
// The Semaphore single-flag endpoint answers HTTP 200 with "failed": true for an unknown flag.
func checkResponse(endpoint Endpoint, body []byte) error {
	if endpoint != SemaphoreEval {
		return nil
	}
	var v struct {
		ErrorCode string `json:"errorCode"`
		Failed    bool   `json:"failed"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	if v.Failed {
		return fmt.Errorf("%w: %s", errEvaluationFailed, v.ErrorCode)
	}

	return nil
}

// Eval implements loadtest.Target.
func (t *Target) Eval(worker, i int) error {
	req, err := t.endpoint.NewRequest(t.relay.BaseURL, t.flagKey, worker*7919+i)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)

		return fmt.Errorf("%w %d", errUnexpectedStatus, resp.StatusCode)
	}
	if want := 1 + btoi(t.h2c); resp.ProtoMajor != want {
		_, _ = io.Copy(io.Discard, resp.Body)

		return fmt.Errorf("%w%d, expected HTTP/%d", errResponseProtocolIsHTTP, resp.ProtoMajor, want)
	}
	if t.endpoint != SemaphoreEval {
		// the body is never inspected: drain it (to reuse the connection) without buffering it,
		// a bulk response is large and would distort the memory measurements of the soak
		_, _ = io.Copy(io.Discard, resp.Body)

		return nil
	}
	body, _ := io.ReadAll(resp.Body)

	return checkResponse(t.endpoint, body)
}

// Extra implements loadtest.Target.
func (t *Target) Extra() map[string]int64 {
	return map[string]int64{
		"sink_requests": t.relay.SinkRequests.Load(),
		"sink_bytes":    t.relay.SinkBytes.Load(),
	}
}

// Close implements loadtest.Target.
func (t *Target) Close() {
	t.client.CloseIdleConnections()
	t.relay.Close()
}

func btoi(b bool) int {
	if b {
		return 1
	}

	return 0
}
