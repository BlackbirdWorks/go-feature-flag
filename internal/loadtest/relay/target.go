package relay

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// Target evaluates flags against a real relay proxy over loopback HTTP.
type Target struct {
	relay    *Relay
	client   *http.Client
	endpoint Endpoint
	flagKey  string
}

// NewTarget starts a relay proxy and returns a soak target for the endpoint.
func NewTarget(o Options, endpoint Endpoint, flagKey string, workers int) (*Target, error) {
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
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
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
