package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"gopkg.in/yaml.v3"
)

// Target evaluates flags against a real relay proxy over loopback HTTP.
type Target struct {
	relay    *Relay
	client   *http.Client
	endpoint Endpoint
	flagKey  string
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
	return &Target{
		relay:    r,
		endpoint: endpoint,
		flagKey:  flagKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        workers * 2,
				MaxIdleConnsPerHost: workers * 2,
				IdleConnTimeout:     time.Minute,
			},
		},
	}, nil
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
		return fmt.Errorf("endpoint %q does not emit events, so exporter %q would never be exercised: use %s or %s",
			endpoint, o.ExporterMode, SemaphoreEval, OFREPEval)
	}
	if endpoint.EmitsEvents() &&
		(o.ExporterMode == loadtest.ExporterSlow || o.ExporterMode == loadtest.ExporterFailing) &&
		!tracksEvents(o.FlagsYAML, flagKey) {
		return fmt.Errorf("flag %q has trackEvents disabled, so exporter %q would never be exercised: "+
			"choose a flag with tracking on", flagKey, o.ExporterMode)
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
		Failed    bool   `json:"failed"`
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("invalid response: %w", err)
	}
	if v.Failed {
		return fmt.Errorf("evaluation failed: %s", v.ErrorCode)
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
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
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
