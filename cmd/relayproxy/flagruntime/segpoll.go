package flagruntime

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"maps"
	"math/big"
	"slices"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

// SegmentPoll configures [RunSegmentVersions].
type SegmentPoll struct {
	// Wait is the clock of the loop. Required.
	Wait Wait
	// Jitter returns a duration in [0, limit): the spread of the first poll of a binding. Nil means no spread.
	Jitter func(limit time.Duration) time.Duration
	// Logger (optional) receives one line for each failure that is new and for each change of the state of a connection
	// (down, then works again). Nil discards.
	Logger *slog.Logger
	// OnError (optional) receives the failures of a poll. A holder that fails the same way as before is reported once.
	OnError func(error)
	// OnConnection (optional) is called when the store of a connection starts failing (down) or works again, never
	// for each evaluation or poll.
	OnConnection func(connection string, down bool)
	// Tick is the resolution of the loop: a binding is polled when its interval is due.
	Tick time.Duration
}

// RandomSpread returns a random duration in [0, limit), zero for a limit that is not positive: the production source of
// [SegmentPoll.Jitter].
func RandomSpread(limit time.Duration) time.Duration {
	if limit <= 0 {
		return 0
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(limit)))
	if err != nil {
		return 0
	}

	return time.Duration(n.Int64())
}

// RunSegmentVersions refreshes the dynamic segments of the current runtime every tick, off the request path, until ctx
// ends (or Wait fails): it reads the version keys of the lookup segments and refreshes the holders of the snapshot
// segments. Each distinct binding is polled once when its interval is due, however many flag sets use it; the bindings
// of one connection are polled in order, and each connection in a pass of its own bounded by the interval, so a store
// that hangs delays no other. The time of the loop is the sum of the ticks it waited, so it needs no wall clock and a
// test that drives Wait drives the polls. The loop is owned by the run context of the relay and joins its passes.
func RunSegmentVersions(ctx context.Context, holder *Holder, poll *SegmentPoll) {
	var (
		now  = time.Unix(0, 0)
		down = map[string]bool{}
	)

	for {
		if poll.Wait(ctx, poll.Tick) != nil {
			return
		}

		now = now.Add(poll.Tick)
		bindings := holder.Load().liveBindings()
		due := map[string][]*segBinding{}

		for _, b := range bindings {
			if b.due(now, poll.Jitter) {
				due[b.connection] = append(due[b.connection], b)
			}
		}

		runPasses(ctx, due, now, poll.reportError(ctx))
		reportConnections(bindings, down, poll.reportConnection(ctx))
	}
}

// reportError logs a failure that is new and passes it to OnError.
func (p *SegmentPoll) reportError(ctx context.Context) func(error) {
	return func(err error) {
		if p.Logger != nil {
			p.Logger.LogAttrs(ctx, slog.LevelWarn, "a segment could not be refreshed", slog.Any("error", err))
		}

		if p.OnError != nil {
			p.OnError(err)
		}
	}
}

// reportConnection logs the change of the state of a connection and passes it to OnConnection.
func (p *SegmentPoll) reportConnection(ctx context.Context) func(string, bool) {
	return func(name string, down bool) {
		if p.Logger != nil {
			if down {
				p.Logger.LogAttrs(ctx, slog.LevelWarn, "a segment store fails: its segments are STORE_ERROR or STALE",
					slog.String("connection", name))
			} else {
				p.Logger.LogAttrs(ctx, slog.LevelInfo, "a segment store works again", slog.String("connection", name))
			}
		}

		if p.OnConnection != nil {
			p.OnConnection(name, down)
		}
	}
}

// runPasses polls the due bindings, one concurrent pass for each connection, and waits for them.
func runPasses(ctx context.Context, due map[string][]*segBinding, now time.Time, onError func(error)) {
	var passes sync.WaitGroup

	for _, list := range due {
		passes.Go(func() {
			bound := list[0].interval
			for _, b := range list {
				bound = min(bound, b.interval)
			}

			pass, cancel := context.WithTimeout(ctx, bound)
			defer cancel()

			for _, b := range list {
				err := b.poll(pass, now)
				if err != nil && onError != nil {
					onError(err)
				}
			}
		})
	}

	passes.Wait()
}

// reportConnections calls report for each connection whose state changed since the last call.
func reportConnections(bindings []*segBinding, down map[string]bool, report func(string, bool)) {
	now := map[string]bool{}

	for _, b := range bindings {
		now[b.connection] = now[b.connection] || b.unhealthy()
	}

	for _, name := range slices.Sorted(maps.Keys(now)) {
		if now[name] != down[name] {
			down[name] = now[name]

			report(name, now[name])
		}
	}
}

// liveBindings returns the distinct bindings that the flag sets of the runtime use, in a stable order.
func (r *Runtime) liveBindings() []*segBinding {
	seen := map[*segBinding]struct{}{}

	var out []*segBinding

	for _, name := range slices.Sorted(maps.Keys(r.FlagSets)) {
		set := r.FlagSets[name]
		if set.Segments == nil || set.Segments.View == nil {
			continue
		}

		for _, group := range set.Segments.View.groups {
			if _, dup := seen[group.bind]; !dup {
				seen[group.bind] = struct{}{}
				out = append(out, group.bind)
			}
		}
	}

	return out
}

// SegmentBindingsStatus is the shared bindings of the dynamic segments that the runtime serves.
type SegmentBindingsStatus struct {
	// Connections is the state of the held sets of each connection, sorted by name.
	Connections []ConnectionSnapshots `json:"connections,omitempty"`
	// Live is the number of bindings that the flag sets of the runtime use: one for each segments object, connection
	// and reading of a missing key, however many flag sets share it.
	Live int `json:"live"`
}

// ConnectionSnapshots counts the held sets (snapshot mode) of one connection by state, and their members.
type ConnectionSnapshots struct {
	// Connection is the name in the bootstrap file.
	Connection string `json:"connection"`
	// Loaded is the number of sets held and current.
	Loaded int `json:"loaded"`
	// Stale is the number of sets held after a refresh failed (or a set over the limit).
	Stale int `json:"stale"`
	// Missing is the number of segments that the store does not have.
	Missing int `json:"missing"`
	// NeverLoaded is the number of segments with no set yet.
	NeverLoaded int `json:"neverLoaded"`
	// Members is the number of members held, all the sets of the connection together.
	Members int `json:"members"`
}

// SegmentBindings reads the state of the bindings of the runtime. It calls no store.
func (r *Runtime) SegmentBindings() SegmentBindingsStatus {
	bindings := r.liveBindings()
	out := SegmentBindingsStatus{Live: len(bindings)}
	byConn := map[string]*ConnectionSnapshots{}

	for _, b := range bindings {
		if len(b.holders) == 0 {
			continue
		}

		conn := byConn[b.connection]
		if conn == nil {
			conn = &ConnectionSnapshots{Connection: b.connection}
			byConn[b.connection] = conn
		}

		for _, h := range b.holders {
			status := h.Status()
			conn.Members += status.Members

			switch {
			case status.LastError == snapshot.ClassMissing:
				conn.Missing++
			case !status.Loaded:
				conn.NeverLoaded++
			case status.LastError != snapshot.ClassNone:
				conn.Stale++
			default:
				conn.Loaded++
			}
		}
	}

	for _, name := range slices.Sorted(maps.Keys(byConn)) {
		out.Connections = append(out.Connections, *byConn[name])
	}

	return out
}

// RefreshVersions polls the bindings of the view that are due at now: one step of the loop for one view, off the
// request path. The error joins the failures; a segment whose version cannot be read keeps its version.
func (v *SegmentView) RefreshVersions(ctx context.Context, now time.Time) error {
	if v == nil {
		return nil
	}

	var errs []error

	for _, group := range v.groups {
		if group.bind.due(now, nil) {
			err := group.bind.poll(ctx, now)
			if err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}
