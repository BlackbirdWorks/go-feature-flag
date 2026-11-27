package stream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/r3labs/sse/v2"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/notifier"
)

// SSEService is the service interface that handles SSE connections.
// It dispatches OFREP flag change events (OpenFeature ADR-0008) scoped to specific
// flagset names so that only clients connected with an API key belonging to a given
// flagset receive the corresponding events.
type SSEService interface {
	// BroadcastFlagChanges sends an OFREP refetchEvaluation event to all clients
	// subscribed to the given flagset stream.
	BroadcastFlagChanges(flagsetName string, diff notifier.DiffCache) error
	// ServeHTTP handles incoming SSE client connections. The request must
	// carry a "stream" query parameter set to the target flagset name.
	ServeHTTP(w http.ResponseWriter, r *http.Request)
	// DisconnectAnonymous closes the streams of the clients without a key whose flag set is not keep; an empty keep
	// closes all of them. A client with a key is never touched.
	DisconnectAnonymous(keep string)
	// Connections is the number of connected clients.
	Connections() int
	// SetOnSubscribe registers a callback invoked when a client subscribes to
	// any stream. The streamID matches the flagset name used in BroadcastFlagChanges.
	SetOnSubscribe(fn func(streamID string))
	// Close shuts down the SSE server and disconnects all clients.
	Close()
}

// AnonymousStreamPrefix starts the stream ID of the clients that have no API key: they have a stream of their own
// next to the one of the flag set, so that the relay can close them when the anonymous access ends.
const AnonymousStreamPrefix = "anonymous:"

// SSEServiceImpl is the implementation of SSEService backed by r3labs/sse.
type SSEServiceImpl struct {
	server *sse.Server
	// activeStreams counts the connected clients per stream, used to send the heartbeats.
	activeStreams map[string]int
	done          chan struct{}
	closeOnce     sync.Once
	// heartbeats joins the heartbeat goroutine: Close returns after it ended.
	heartbeats      sync.WaitGroup
	muActiveStreams sync.Mutex
}

// NewSSEService creates a new SSEService backed by r3labs/sse.
func NewSSEService(opts ...Option) *SSEServiceImpl {
	server := sse.New()
	server.AutoReplay = false
	server.AutoStream = true

	svc := &SSEServiceImpl{
		server:        server,
		activeStreams: map[string]int{},
		done:          make(chan struct{}),
	}
	if interval := newOptions(opts...).sseHeartbeatInterval; interval > 0 {
		svc.heartbeats.Go(func() { svc.sendHeartbeats(interval) })
	}

	return svc
}

// BroadcastFlagChanges only notifies that the flags changed, the diff is not sent:
// providers re-fetch their evaluations when they receive the event.
func (s *SSEServiceImpl) BroadcastFlagChanges(flagsetName string, _ notifier.DiffCache) error {
	now := clock.Now()

	data, err := json.Marshal(model.OFREPSSEEvent{
		Type:         model.OFREPSSEEventTypeRefetchEvaluation,
		LastModified: now.Unix(),
	})
	if err != nil {
		return fmt.Errorf("sse: failed to marshal OFREP event for stream %q: %w", flagsetName, err)
	}

	for _, streamID := range []string{flagsetName, AnonymousStreamPrefix + flagsetName} {
		s.server.Publish(streamID, &sse.Event{
			ID:    []byte(strconv.FormatInt(now.UnixNano(), 10)),
			Event: []byte("message"),
			Data:  data,
		})
	}

	return nil
}

// Connections is the number of connected clients.
func (s *SSEServiceImpl) Connections() int {
	s.muActiveStreams.Lock()
	defer s.muActiveStreams.Unlock()

	total := 0
	for _, count := range s.activeStreams {
		total += count
	}

	return total
}

// DisconnectAnonymous closes the streams of the clients without a key whose flag set is not keep.
func (s *SSEServiceImpl) DisconnectAnonymous(keep string) {
	s.muActiveStreams.Lock()

	var revoked []string

	for streamID := range s.activeStreams {
		name, anonymous := strings.CutPrefix(streamID, AnonymousStreamPrefix)
		if anonymous && (name != keep || keep == "") {
			revoked = append(revoked, streamID)
		}
	}

	s.muActiveStreams.Unlock()

	for _, streamID := range revoked {
		s.server.RemoveStream(streamID)
	}
}

func (s *SSEServiceImpl) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	streamID := r.URL.Query().Get("stream")

	s.trackStream(streamID, 1)
	defer s.trackStream(streamID, -1)

	if flusher, ok := w.(http.Flusher); ok {
		w = &connectedCommentWriter{ResponseWriter: w, flusher: flusher}
	}

	s.server.ServeHTTP(w, r)
}

// SetOnSubscribe sets the callback called when a client subscribes to a stream.
func (s *SSEServiceImpl) SetOnSubscribe(fn func(streamID string)) {
	s.server.OnSubscribe = func(streamID string, _ *sse.Subscriber) {
		fn(strings.TrimPrefix(streamID, AnonymousStreamPrefix))
	}
}

// Close closes all the SSE streams.
func (s *SSEServiceImpl) Close() {
	s.closeOnce.Do(func() { close(s.done) })
	s.heartbeats.Wait()
	s.server.Close()
}

func (s *SSEServiceImpl) trackStream(streamID string, delta int) {
	s.muActiveStreams.Lock()
	defer s.muActiveStreams.Unlock()

	s.activeStreams[streamID] += delta
	if s.activeStreams[streamID] <= 0 {
		delete(s.activeStreams, streamID)
	}
}

// sendHeartbeats periodically sends an SSE comment on every stream with connected clients,
// so that proxies and load balancers do not close the connections while no flag changes.
func (s *SSEServiceImpl) sendHeartbeats(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.muActiveStreams.Lock()

			streamIDs := make([]string, 0, len(s.activeStreams))
			for streamID := range s.activeStreams {
				streamIDs = append(streamIDs, streamID)
			}
			s.muActiveStreams.Unlock()

			for _, streamID := range streamIDs {
				s.server.TryPublish(streamID, &sse.Event{Comment: []byte("heartbeat")})
			}
		}
	}
}

// connectedCommentWriter sends an SSE comment right after the response headers.
// Some browsers (e.g. Firefox) fire the EventSource "open" event only once the first
// bytes of the body are received: without it, clients stay "connecting" until the first event.
type connectedCommentWriter struct {
	http.ResponseWriter

	flusher http.Flusher
	status  int
	sent    bool
}

func (w *connectedCommentWriter) WriteHeader(statusCode int) {
	w.status = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *connectedCommentWriter) Flush() {
	if !w.sent && w.status == http.StatusOK {
		w.sent = true
		_, _ = io.WriteString(w.ResponseWriter, ": connected\n\n")
	}

	w.flusher.Flush()
}
