//go:build docker

package manifestreader_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	prom "github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

var errRefused = errors.New("connection refused (simulated)")

// switchWire counts every HTTP request that is sent, and fails them while it is down: the network of the replica
// to the store is cut.
type switchWire struct {
	seen []wireRequest
	mu   sync.Mutex
	down atomic.Bool
	// serverErrors is the number of requests that are answered 503 before the emulator is asked again.
	serverErrors atomic.Int32
}

func (w *switchWire) RoundTrip(req *http.Request) (*http.Response, error) {
	w.mu.Lock()
	w.seen = append(w.seen, wireRequest{Method: req.Method, Path: req.URL.Path})
	w.mu.Unlock()

	if w.down.Load() {
		return nil, errRefused
	}

	if w.serverErrors.Add(-1) >= 0 {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Request: req,
			Header: http.Header{"Content-Type": []string{"application/xml"}},
			Body: io.NopCloser(strings.NewReader(
				"<Error><Code>ServiceUnavailable</Code><Message>try again</Message></Error>")),
		}, nil
	}

	return http.DefaultTransport.RoundTrip(req)
}

func (w *switchWire) take() []wireRequest {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := w.seen
	w.seen = nil

	return out
}

// teeObserver feeds the two observers of the request metric at once.
type teeObserver struct{ observers []connection.RequestObserver }

func (t teeObserver) Observe(conn, operation, result string) {
	for _, obs := range t.observers {
		obs.Observe(conn, operation, result)
	}
}

// counters are the three counts of the requests of a replica that must agree: the wire, the observer of the
// connection factory, and the Prometheus counter.
type counters struct {
	wire *switchWire
	obs  *observed
	reg  *prom.Registry
}

// take returns the wire requests and the observations since the last call, and the totals of the counter.
func (c *counters) take(t *testing.T) ([]wireRequest, []observation, map[string]float64) {
	t.Helper()

	families, err := c.reg.Gather()
	require.NoError(t, err)

	prometheus := map[string]float64{}

	for _, family := range families {
		if family.GetName() != "semaphore_s3_requests_total" {
			continue
		}

		for _, metricRow := range family.GetMetric() {
			prometheus[labelOf(metricRow, "result")] += metricRow.GetCounter().GetValue()
		}
	}

	return c.wire.take(), c.obs.take(), prometheus
}

func labelOf(row *dto.Metric, name string) string {
	for _, label := range row.GetLabel() {
		if label.GetName() == name {
			return label.GetValue()
		}
	}

	return ""
}

// stateRig is a reader on an emulator with a state directory and the three counters.
type stateRig struct {
	*rig
	counters

	state  *stateRun
	srv    *gopherstack.Server
	prom   map[string]float64
	bucket string
}

// newStateRig starts a reader on the bucket at endpoint (the emulator or a proxy of it) with a state directory
// and a fixed jitter source. The connection makes one attempt per request, so a failed request is one request.
func newStateRig(
	t *testing.T, srv *gopherstack.Server, endpoint, bucket, dir string, jitter float64,
) *stateRig {
	t.Helper()

	return newStateRigWithRetry(t, srv, endpoint, bucket, dir, jitter, connection.Retry{MaxAttempts: 1})
}

// newStateRigWithRetry is newStateRig with the retry settings of the connection.
func newStateRigWithRetry(
	t *testing.T, srv *gopherstack.Server, endpoint, bucket, dir string, jitter float64, retry connection.Retry,
) *stateRig {
	t.Helper()

	wire, obs, reg := &switchWire{}, &observed{}, prom.NewRegistry()

	s3Requests, err := metric.NewS3Requests(reg)
	require.NoError(t, err)

	fac := connection.New(
		connection.WithRoundTripper(
			wire,
		),
		connection.WithObserver(teeObserver{[]connection.RequestObserver{obs, s3Requests}}),
	)
	require.NoError(t, fac.Set(connControl, &connection.S3Spec{
		Endpoint: endpoint, Region: gopherstack.Region, Bucket: bucket, PathStyle: true,
		Retry: retry,
		Credentials: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret(gopherstack.AccessKey),
			SecretAccessKey: connection.NewSecret(gopherstack.SecretKey),
		},
	}))

	state := openState(t, dir, nil)
	store := manifestreader.NewS3Store(fac, connControl, bucket, "")
	rdr := launchRig(t, store, e2eConfig(), manifestreader.WithHooks(state.store.Hooks()), fixedJitter(jitter))

	return &stateRig{
		rig: rdr, wire: wire, obs: obs, reg: reg, state: state, srv: srv, bucket: bucket,
		prom: map[string]float64{},
	}
}

// agreeNow asserts that the three counts name the same requests since the last call: wantRequests in all, with
// the given results. The wire count (a RoundTripper under the SDK) is the independent one: the Prometheus counter is
// fed by the observer of the connection factory, so those two agree by construction, and the check on the counter
// proves the plumbing (the names and the results), not a second count.
func (s *stateRig) agreeNow(t *testing.T, wantRequests int, wantResults map[string]float64) []observation {
	t.Helper()

	wire, obs, totals := s.counters.take(t)

	assert.Len(t, wire, wantRequests, "wire")
	assert.Len(t, obs, wantRequests, "observer")

	delta := map[string]float64{}
	sum := 0.0

	for result, value := range totals {
		if diff := value - s.prom[result]; diff != 0 {
			delta[result] = diff
			sum += diff
		}
	}

	assert.InDelta(t, float64(wantRequests), sum, 0, "prometheus counter")
	assert.Equal(t, wantResults, delta, "prometheus counter by result")

	for _, req := range wire {
		assert.Equal(t, http.MethodGet, req.Method, "no HEAD, no LIST")
	}

	s.prom = totals

	return obs
}

func publishObjects(t *testing.T, srv *gopherstack.Server, bucket string, gen uint64, objs ...obj) {
	t.Helper()

	for _, o := range objs {
		srv.PutObject(t, bucket, keyOf(t, o), []byte(o.content))
	}

	srv.PutObject(t, bucket, manifestKey, mustMarshal(t, manifestOf(t, gen, objs...)))
}

func TestE2E_RequestBudgetOnTheWire(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "mrb")
	objs := threeObjects()
	publishObjects(t, srv, bucket, 1, objs...)

	rdr := newStateRig(t, srv, srv.Endpoint, bucket, filepath.Join(t.TempDir(), "state"), 0)
	rdr.settle()

	// startup: the manifest and the three objects; the wire, the observer and the counter agree
	rdr.agreeNow(t, 1+len(objs), map[string]float64{connection.ResultOK: float64(1 + len(objs))})

	// steady state: exactly one conditional GET per round, nothing else
	const rounds = 10

	for range rounds {
		rdr.round()
	}

	rdr.agreeNow(t, rounds, map[string]float64{connection.ResultNotModified: rounds})

	// one changed object: the manifest and that object
	changed := obj{name: nameAlpha, content: contentAlphaChanged}
	publishObjects(t, srv, bucket, 2, changed, objs[1], objs[2])
	rdr.round()

	rdr.agreeNow(t, 2, map[string]float64{connection.ResultOK: 2})

	rdr.state.flush()
}

func TestE2E_RestartWithTheStateDirectoryCostsOneRequest(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "mrr")
	objs := threeObjects()
	publishObjects(t, srv, bucket, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	first := newStateRig(t, srv, srv.Endpoint, bucket, dir, 0)
	first.settle()
	first.state.flush()
	first.stop()
	first.state.close()

	// the restart: the snapshot is served at once, and the whole cost is one conditional GET answered 304
	second := newStateRig(t, srv, srv.Endpoint, bucket, dir, 0)
	second.settle()

	snap := second.snapshot()
	require.NotNil(t, snap)

	for _, o := range objs {
		assert.Equal(t, okState(t, o), snap.Objects[o.name])
	}

	second.agreeNow(t, 1, map[string]float64{connection.ResultNotModified: 1})
	assert.Equal(t, manifestreader.PollNotModified, second.reader.Status().Poll.Result)
}

// stoppableProxy forwards TCP connections to the emulator until it is stopped: after that the address refuses
// connections, as the address of a stopped emulator does. (The emulator of the tests is shared by every test of
// the package and cannot be stopped.)
type stoppableProxy struct {
	listener net.Listener
	conns    []net.Conn
	mu       sync.Mutex
}

func startProxy(t *testing.T, target string) *stoppableProxy {
	t.Helper()

	parsed, err := url.Parse(target)
	require.NoError(t, err)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	proxy := &stoppableProxy{listener: listener}

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			upstream, dialErr := (&net.Dialer{Timeout: wait}).DialContext(t.Context(), "tcp", parsed.Host)
			if dialErr != nil {
				_ = conn.Close()

				continue
			}

			proxy.mu.Lock()
			proxy.conns = append(proxy.conns, conn, upstream)
			proxy.mu.Unlock()

			go func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close() }()
			go func() { _, _ = io.Copy(conn, upstream); _ = conn.Close() }()
		}
	}()

	t.Cleanup(proxy.stop)

	return proxy
}

func (p *stoppableProxy) endpoint() string { return "http://" + p.listener.Addr().String() }

func (p *stoppableProxy) stop() {
	_ = p.listener.Close()

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conn := range p.conns {
		_ = conn.Close()
	}
}

func TestE2E_RestartWithTheStoreStoppedServesTheSnapshot(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "mrs")
	objs := threeObjects()
	publishObjects(t, srv, bucket, 1, objs...)

	proxy := startProxy(t, srv.Endpoint)
	dir := filepath.Join(t.TempDir(), "state")

	first := newStateRig(t, srv, proxy.endpoint(), bucket, dir, 0)
	first.settle()
	first.state.flush()
	first.stop()
	first.state.close()

	// the emulator goes away
	proxy.stop()

	second := newStateRig(t, srv, proxy.endpoint(), bucket, dir, 0)
	second.settle()

	snap := second.snapshot()
	require.NotNil(t, snap, "the saved content is served although nothing answers")

	for _, o := range objs {
		assert.Equal(t, okState(t, o), snap.Objects[o.name])
	}

	// the one request of the round failed; the observer, the wire and the counter agree on it
	second.agreeNow(t, 1, map[string]float64{connection.ResultError: 1})

	status := second.reader.Status()
	assert.Equal(t, manifestreader.PollError, status.Poll.Result)
	assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
	assert.Equal(t, 1, status.Backoff.Failures)
}

func TestE2E_OutageThenRecoveryResetsTheBackoff(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "mro")
	objs := threeObjects()
	publishObjects(t, srv, bucket, 1, objs...)

	// jitter 0.75: after n failures the wait is the interval or 0.75 x min(5 min, 60 s x 2^n)
	rdr := newStateRig(t, srv, srv.Endpoint, bucket, filepath.Join(t.TempDir(), "state"), 0.75)
	rdr.settle()
	rdr.agreeNow(t, 1+len(objs), map[string]float64{connection.ResultOK: float64(1 + len(objs))})

	rdr.wire.down.Store(true)

	for range 4 {
		rdr.round()
	}

	assert.Equal(t, []time.Duration{sec(60), sec(90), sec(180), sec(225), sec(225)}, rdr.sched.waited())
	assert.Equal(t, manifestreader.BackoffStatus{Failures: 4, Wait: sec(225)}, rdr.reader.Status().Backoff)

	// each failed round is exactly one request: nothing is added to a failing manifest GET
	rdr.agreeNow(t, 4, map[string]float64{connection.ResultError: 4})

	// the network is back: the first poll is a 304 and the backoff is over
	rdr.wire.down.Store(false)
	rdr.round()

	assert.Equal(t, manifestreader.BackoffStatus{Wait: sec(60)}, rdr.reader.Status().Backoff)
	assert.Equal(t, manifestreader.PollNotModified, rdr.reader.Status().Poll.Result)
	rdr.agreeNow(t, 1, map[string]float64{connection.ResultNotModified: 1})

	rdr.round()
	assert.Equal(t, sec(60), rdr.sched.waited()[len(rdr.sched.waited())-1])
}

// TestE2E_SDKRetryIsOnePollAndTwoRequests turns the retries of the SDK on: a 503 followed by a 200 is one poll of the
// reader and two requests. The wire (a counting RoundTripper under the SDK) and the observer of the connection
// factory are two independent counts and they agree. The Prometheus counter is fed by that observer, so it agrees
// by construction: the wire is the independent count.
func TestE2E_SDKRetryIsOnePollAndTwoRequests(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	bucket := srv.CreateBucket(t, "mrt")
	objs := threeObjects()
	publishObjects(t, srv, bucket, 1, objs...)

	rdr := newStateRigWithRetry(t, srv, srv.Endpoint, bucket, filepath.Join(t.TempDir(), "state"), 0,
		connection.Retry{MaxAttempts: 3, MaxBackoff: time.Millisecond})
	rdr.settle()
	rdr.agreeNow(t, 1+len(objs), map[string]float64{connection.ResultOK: float64(1 + len(objs))})

	pollsBefore := rdr.reader.Status().Polls

	rdr.wire.serverErrors.Store(1)
	rdr.round()

	status := rdr.reader.Status()
	assert.Equal(t, pollsBefore+1, status.Polls, "one poll")
	assert.Equal(t, manifestreader.PollNotModified, status.Poll.Result, "which succeeded")
	assert.Zero(t, status.Backoff.Failures, "a request that the SDK retried is not a failed poll")

	obs := rdr.agreeNow(t, 2, map[string]float64{connection.ResultServerError: 1, connection.ResultNotModified: 1})
	assert.Equal(t, []observation{
		{connControl, connection.OperationGet, connection.ResultServerError},
		{connControl, connection.OperationGet, connection.ResultNotModified},
	}, obs, "two requests on the wire, two observations (one per attempt), and the counter counted both")
}
