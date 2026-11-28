//go:build docker

package segments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainerredis "github.com/testcontainers/testcontainers-go/modules/redis"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/internal/loadtest"
	"github.com/blackbirdworks/semaphore/internal/loadtest/relay"
)

const (
	valkeyImage = "docker.io/valkey/valkey:8-alpine"
	// waitFor bounds every wait of the tests: a state change takes a few poll intervals (100 ms) and the timeouts under
	// test (300 ms), so a wait that lasts this long is a failure and not a slow machine.
	waitFor      = 20 * time.Second
	tick         = 25 * time.Millisecond
	statusKey    = "semaphore_segmentStatus"
	labelResult  = "result"
	storeErr     = "STORE_ERROR"
	staleStatus  = "STALE"
	stopGrace    = 10 * time.Second
	clientBound  = 30 * time.Second
	segHeader    = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n"
	connName     = "audiences"
	connSettings = "forceSingleClient: true, lookupTimeout: 300ms, timeouts: {connect: 500ms, io: 1s}"
)

var errEval = errors.New("evaluation failed")

// store is a real Valkey container and a client of it that seeds the sets and reads its command counters.
type store struct {
	container testcontainers.Container
	client    valkey.Client
	address   string
	paused    atomic.Bool
}

func startStore(tb testing.TB) *store {
	tb.Helper()

	container, err := testcontainerredis.Run(tb.Context(), valkeyImage)
	testcontainers.CleanupContainer(tb, container)
	require.NoError(tb, err)

	s := &store{container: container}
	s.connect(tb)

	return s
}

// connect (re)opens the client of the test at the current address of the container.
func (s *store) connect(tb testing.TB) {
	tb.Helper()

	address, err := s.container.Endpoint(tb.Context(), "")
	require.NoError(tb, err)

	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{address}, DisableCache: true})
	require.NoError(tb, err)
	tb.Cleanup(client.Close)

	s.client, s.address = client, address
}

// stop is the kill of the chaos tests: the container stops and its client connections are gone.
func (s *store) stop(tb testing.TB) {
	tb.Helper()

	s.client.Close()

	grace := stopGrace
	require.NoError(tb, s.container.Stop(tb.Context(), &grace))
}

func (s *store) start(tb testing.TB) {
	tb.Helper()

	require.NoError(tb, s.container.Start(tb.Context()))
	s.connect(tb)
}

// pause freezes the container (docker pause: the connections stay open and nothing answers, no reset). The cleanup
// unpauses it when the test did not, so that the container can be removed.
func (s *store) pause(tb testing.TB) {
	tb.Helper()

	s.docker(tb, "pause")
	s.paused.Store(true)
	tb.Cleanup(func() {
		if s.paused.Load() {
			s.docker(tb, "unpause")
		}
	})
}

func (s *store) unpause(tb testing.TB) {
	tb.Helper()

	s.docker(tb, "unpause")
	s.paused.Store(false)
}

// docker runs the docker command line on the container (testcontainers has no call for pause and unpause); the
// context outlives the test so that a cleanup can use it.
func (s *store) docker(tb testing.TB, verb string) {
	tb.Helper()

	ctx := context.WithoutCancel(tb.Context())
	out, err := exec.CommandContext(ctx, "docker", verb, s.container.GetContainerID()).CombinedOutput()
	require.NoError(tb, err, "docker %s: %s", verb, out)
}

// add writes the members to a set and bumps its version key, so a snapshot pulls it again.
func (s *store) add(tb testing.TB, key string, members ...string) {
	tb.Helper()

	cmd := s.client.B().Sadd().Key(key).Member(members...).Build()
	require.NoError(tb, s.client.Do(tb.Context(), cmd).Error())
	s.version(tb, key)
}

func (s *store) version(tb testing.TB, key string) {
	tb.Helper()

	value := strconv.FormatInt(time.Now().UnixNano(), 10)
	cmd := s.client.B().Set().Key(key + "-version").Value(value).Build()
	require.NoError(tb, s.client.Do(tb.Context(), cmd).Error())
}

// seed writes count members "<prefix><n>" to key with pipelined SADD batches, and sets the version key.
func (s *store) seed(tb testing.TB, key, prefix string, count int) {
	tb.Helper()

	const (
		perCommand = 5000
		perPipe    = 40
	)

	var cmds valkey.Commands

	flush := func() {
		for _, res := range s.client.DoMulti(tb.Context(), cmds...) {
			require.NoError(tb, res.Error())
		}

		cmds = cmds[:0]
	}

	for from := 0; from < count; from += perCommand {
		members := make([]string, 0, perCommand)
		for n := from; n < min(count, from+perCommand); n++ {
			members = append(members, prefix+strconv.Itoa(n))
		}

		cmds = append(cmds, s.client.B().Sadd().Key(key).Member(members...).Build())
		if len(cmds) == perPipe {
			flush()
		}
	}

	if len(cmds) > 0 {
		flush()
	}

	s.version(tb, key)
}

// calls returns the number of calls of each command that the server counted (INFO commandstats), by command name.
func (s *store) calls(tb testing.TB) map[string]int64 {
	tb.Helper()

	text, err := s.client.Do(tb.Context(), s.client.B().Info().Section("commandstats").Build()).ToString()
	require.NoError(tb, err)

	out := map[string]int64{}

	for line := range strings.SplitSeq(text, "\n") {
		name, rest, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(name, "cmdstat_") {
			continue
		}

		raw, _, _ := strings.Cut(strings.TrimPrefix(rest, "calls="), ",")
		n, convErr := strconv.ParseInt(raw, 10, 64)
		require.NoError(tb, convErr, line)

		out[strings.TrimPrefix(name, "cmdstat_")] = n
	}

	return out
}

// syncBuffer is a log destination that the relay writes from many goroutines.
type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// rig is a relay proxy that serves the flags from a real store.
type rig struct {
	*relay.Relay

	logs   *syncBuffer
	client *http.Client
}

type rigOptions struct {
	// address of the store as the relay dials it (the proxy, or the container).
	address string
	// connection settings after the type and the address; empty is connSettings.
	settings string
	// resolver is the segments section of the bootstrap file.
	resolver string
	flags    string
	segments string
}

func startRig(tb testing.TB, opts *rigOptions) *rig {
	tb.Helper()

	if opts.settings == "" {
		opts.settings = connSettings
	}

	logs := &syncBuffer{}

	r, err := relay.StartWithStore(&relay.Options{FlagsYAML: loadtest.FlagsYAML() + opts.flags, Listen: true},
		&relay.StoreOptions{
			SegmentsDoc: segHeader + opts.segments,
			Connections: "  " + connName + ": {type: valkey, addresses: [\"" + opts.address + "\"], " + opts.settings + "}\n",
			Sections:    opts.resolver,
			Logger:      slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})),
		})
	require.NoError(tb, err)
	tb.Cleanup(r.Close)

	transport := &http.Transport{MaxIdleConnsPerHost: 64}
	tb.Cleanup(transport.CloseIdleConnections)

	return &rig{Relay: r, logs: logs, client: &http.Client{Transport: transport, Timeout: clientBound}}
}

// answer is what the evaluation of a flag returns, for the fields the tests read.
type answer struct {
	Metadata map[string]any `json:"metadata"`
	Value    bool           `json:"value"`
}

func (a answer) status() string {
	status, _ := a.Metadata[statusKey].(string)

	return status
}

// eval evaluates a boolean flag for a targeting key over HTTP (OFREP).
func (r *rig) eval(flag, key string) (answer, error) {
	body := `{"context":{"targetingKey":"` + key + `"}}`

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		r.BaseURL+"/ofrep/v1/evaluate/flags/"+flag, strings.NewReader(body))
	if err != nil {
		return answer{}, fmt.Errorf("request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", relay.APIKey)

	resp, err := r.client.Do(req)
	if err != nil {
		return answer{}, fmt.Errorf("evaluate: %w", err)
	}

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return answer{}, fmt.Errorf("read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return answer{}, fmt.Errorf("%w: status %d: %s", errEval, resp.StatusCode, raw)
	}

	var got answer

	err = json.Unmarshal(raw, &got)

	return got, err
}

// get returns the status code of a GET of the relay.
func (r *rig) get(tb testing.TB, path string) (int, []byte) {
	tb.Helper()

	req, err := http.NewRequestWithContext(tb.Context(), http.MethodGet, r.BaseURL+path, http.NoBody)
	require.NoError(tb, err)

	resp, err := r.client.Do(req)
	require.NoError(tb, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(tb, err)

	return resp.StatusCode, raw
}

func (r *rig) info(tb testing.TB) *model.ConfigInfo {
	tb.Helper()

	code, raw := r.get(tb, "/info")
	require.Equal(tb, http.StatusOK, code)

	var info model.InfoResponse

	require.NoError(tb, json.Unmarshal(raw, &info))
	require.NotNil(tb, info.Config)

	return info.Config
}

// storeErrors is the number of dynamic segments that /info reports in store error, and the state of the connection.
func (r *rig) storeErrors(tb testing.TB) (int, string) {
	tb.Helper()

	cfg := r.info(tb)
	state := ""

	if len(cfg.SegmentStores) > 0 {
		state = cfg.SegmentStores[0].State
	}

	for i := range cfg.FlagSets {
		if seg := cfg.FlagSets[i].Segments; seg != nil && seg.DynamicStates != nil {
			return seg.DynamicStates.StoreError, state
		}
	}

	return 0, state
}

func (r *rig) assertReady(tb testing.TB) {
	tb.Helper()

	code, _ := r.get(tb, "/readyz")
	assert.Equal(tb, http.StatusOK, code, "the relay stays ready")
}

// awaitAnswer evaluates until want accepts the answer, and fails with msg and the last answer when it never does.
func (r *rig) awaitAnswer(tb testing.TB, flag, key, msg string, want func(answer) bool) {
	tb.Helper()

	require.EventuallyWithT(tb, func(collect *assert.CollectT) {
		got, err := r.eval(flag, key)
		if assert.NoError(collect, err, msg) {
			assert.True(collect, want(got), "%s: last answer %+v", msg, got)
		}
	}, waitFor, tick, msg)
}

func isMember(a answer) bool   { return a.Value && a.status() == "" }
func notMember(a answer) bool  { return !a.Value && a.status() == "" }
func isStoreErr(a answer) bool { return !a.Value && a.status() == storeErr }

// isStale is a member of the last good set of a snapshot that the store cannot refresh.
func isStale(a answer) bool { return a.Value && a.status() == staleStatus }

// sample is one metric family of the registry of the relay, summed over the series that carry all the labels.
func (r *rig) metric(tb testing.TB, name string, labels map[string]string) (float64, uint64) {
	tb.Helper()

	var (
		sum   float64
		count uint64
	)

	families, err := r.Metrics.Registry.Gather()
	require.NoError(tb, err)

	for _, family := range families {
		if family.GetName() != name {
			continue
		}

		for _, series := range family.GetMetric() {
			if !hasLabels(series, labels) {
				continue
			}

			switch {
			case series.GetHistogram() != nil:
				sum += series.GetHistogram().GetSampleSum()
				count += series.GetHistogram().GetSampleCount()
			case series.GetCounter() != nil:
				sum += series.GetCounter().GetValue()
			case series.GetGauge() != nil:
				sum += series.GetGauge().GetValue()
			}
		}
	}

	return sum, count
}

func hasLabels(series *dto.Metric, want map[string]string) bool {
	for name, value := range want {
		found := false

		for _, label := range series.GetLabel() {
			if label.GetName() == name && label.GetValue() == value {
				found = true
			}
		}

		if !found {
			return false
		}
	}

	return true
}
