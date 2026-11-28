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
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

// SegmentPoll configures [RunSegmentVersions].
type SegmentPoll struct {
	// Wait is the clock of the loop. Required.
	Wait Wait
	// Jitter returns a duration in [0, limit): the spread of the first poll of a binding. Nil means no spread.
	Jitter func(limit time.Duration) time.Duration
	// Logger (optional) receives one line for each change of the state of a connection (down, then works again), and one
	// for a set that fails while its store works. The failures of a store that is down are detail (debug). Nil discards.
	Logger *slog.Logger
	// OnError (optional) receives the failures of a poll. A holder that fails the same way as before is reported once.
	// It is called from the pass of a connection, so it may run beside another call.
	OnError func(error)
	// OnPass (optional) is called with true when the loop starts the pass of a connection and with false when the pass
	// ends: for a test or a metric, never on the request path.
	OnPass func(connection string, running bool)
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
// segments. Each distinct binding is polled once when its interval is due, however many flag sets use it. The bindings
// of one connection are polled in order in a pass of their own; the loop never waits for a pass, and a connection whose
// pass still runs is skipped, so a store that hangs delays no other connection and there is at most one pass for each
// connection. Each holder refresh has its own deadline ([DefaultPullTimeout]). The time of the loop is the sum of the
// ticks it waited, so it needs no wall clock and a test that drives Wait drives the polls. The loop is owned by the run
// context of the relay; the passes end with it, and it returns after them.
func RunSegmentVersions(ctx context.Context, holder *Holder, poll *SegmentPoll) {
	run := &passRunner{poll: poll, running: map[string]*atomic.Bool{}}
	now := time.Unix(0, 0)

	defer run.passes.Wait()

	for {
		if poll.Wait(ctx, poll.Tick) != nil {
			return
		}

		now = now.Add(poll.Tick)
		bindings := holder.Load().liveBindings()
		due := map[string][]*segBinding{}

		for _, b := range bindings {
			if !run.busy(b.connection) && b.due(now, poll.Jitter) {
				due[b.connection] = append(due[b.connection], b)
			}
		}

		byConn := map[string][]*segBinding{}
		for _, b := range bindings {
			byConn[b.connection] = append(byConn[b.connection], b)
		}

		for _, name := range slices.Sorted(maps.Keys(due)) {
			run.start(ctx, name, due[name], byConn[name], now)
		}

		for _, name := range slices.Sorted(maps.Keys(byConn)) {
			run.report(ctx, name, byConn[name])
		}
	}
}

// passRunner starts the passes of the loop and keeps what they share.
type passRunner struct {
	poll    *SegmentPoll
	running map[string]*atomic.Bool // only the loop touches the map
	down    map[string]bool
	passes  sync.WaitGroup
	mu      sync.Mutex // guards down
}

func (r *passRunner) busy(connection string) bool {
	flag := r.running[connection]

	return flag != nil && flag.Load()
}

// start runs the pass of a connection: its due bindings in order, one after the other.
func (r *passRunner) start(
	ctx context.Context, connection string, list, all []*segBinding, now time.Time,
) {
	flag := r.running[connection]
	if flag == nil {
		flag = &atomic.Bool{}
		r.running[connection] = flag
	}

	flag.Store(true)

	if r.poll.OnPass != nil {
		r.poll.OnPass(connection, true)
	}

	r.passes.Go(func() {
		defer func() {
			flag.Store(false)

			if r.poll.OnPass != nil {
				r.poll.OnPass(connection, false)
			}
		}()

		var errs []error

		for _, b := range list {
			err := b.poll(ctx, now)
			if err != nil {
				errs = append(errs, err)
			}
		}

		r.reportErrors(ctx, list, errs)
		r.report(ctx, connection, all)
	})
}

// reportErrors passes the failures of a pass to OnError. A store that fails writes its one line (see
// report), and the failure of each set is detail; when the store works, a set that fails is a line of its own.
func (r *passRunner) reportErrors(ctx context.Context, list []*segBinding, errs []error) {
	level := slog.LevelWarn

	for _, b := range list {
		if b.unhealthy() {
			level = slog.LevelDebug
		}
	}

	for _, err := range errs {
		if r.poll.Logger != nil {
			r.poll.Logger.LogAttrs(ctx, level, "a segment could not be refreshed", slog.Any("error", err))
		}

		if r.poll.OnError != nil {
			r.poll.OnError(err)
		}
	}
}

// report logs and reports the connection when its state changed since the last call: down when a binding of it fails.
func (r *passRunner) report(ctx context.Context, name string, bindings []*segBinding) {
	down := slices.ContainsFunc(bindings, (*segBinding).unhealthy)

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.down == nil {
		r.down = map[string]bool{}
	}

	if down == r.down[name] {
		return
	}

	r.down[name] = down
	r.poll.reportConnection(ctx)(name, down)
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
