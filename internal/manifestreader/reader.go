package manifestreader

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// ErrAlreadyRunning is returned by a second call of [Reader.Run].
var ErrAlreadyRunning = errors.New("manifestreader: reader already running")

// Reader polls one manifest and fetches its objects. Build it with [New], start it once with [Reader.Run].
//
// Everything below the "poller" comment is touched by the polling goroutine only; other goroutines use
// [Reader.Snapshots] and [Reader.Status].
type Reader struct {
	hooks    Hooks
	lastSync time.Time
	store    ObjectStore
	sched    Scheduler
	cache    map[string][]byte
	last     map[string]pubKey
	status   atomic.Pointer[Status]
	jitter   func() float64
	stores   map[string]ObjectStore
	snaps    chan *Snapshot
	logged   map[string]string
	held     *held
	log      *slog.Logger
	names    map[string]*nameState
	lastPoll string
	etag     string
	failures []ObjectFailure
	manifest ManifestStatus
	cfg      Config
	counts   Counts
	lastGen  uint64
	polls    uint64
	notMod   uint64
	started  atomic.Bool
}

// held is the accepted manifest.
type held struct {
	sums       map[string]string
	channel    string
	raw        []byte
	objects    []controlplane.ManifestObject
	generation uint64
}

// good is the last verified content of a name.
type good struct {
	kind   string
	format string
	sum    controlplane.Checksum
	bytes  []byte
}

// nameState is what the reader knows of one object name.
type nameState struct {
	good      *good
	entry     controlplane.ManifestObject
	state     State
	reason    string
	retryable bool
}

// pubKey is what makes a published object differ from another.
type pubKey struct {
	state  State
	reason string
	sum    string
}

// New returns a reader of store with cfg.
func New(
	store ObjectStore,
	cfg Config, //nolint:gocritic // a value by design: callers pass a literal and it is copied once
	opts ...Option,
) (*Reader, error) {
	err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}

	reader := &Reader{
		store:  store,
		cfg:    cfg,
		log:    slog.New(slog.DiscardHandler),
		sched:  timerScheduler{},
		jitter: defaultJitter,
		snaps:  make(chan *Snapshot, 1),
		cache:  map[string][]byte{},
		names:  map[string]*nameState{},
		logged: map[string]string{},
		manifest: ManifestStatus{
			Outcome: OutcomeNone,
		},
	}

	for _, opt := range opts {
		opt(reader)
	}

	reader.status.Store(&Status{Poll: PollStatus{Result: PollNone}, Manifest: reader.manifest})

	return reader, nil
}

// Snapshots returns the channel of snapshots. It has room for one: a snapshot that is not consumed when the
// next one is ready is replaced by it (latest wins) and the poller never blocks. The channel is closed when
// [Reader.Run] returns.
func (r *Reader) Snapshots() <-chan *Snapshot { return r.snaps }

// Status returns the state at the end of the last round. It never blocks the poller.
func (r *Reader) Status() Status { return *r.status.Load() }

// Run polls until ctx is done and returns nil then. It runs the rounds on the calling goroutine; object
// downloads run on a bounded group of workers that ends with each round, so no goroutine outlives Run. The
// first round starts at once; the others follow the poll interval with jitter. A failed round is simply tried
// again at the next interval.
func (r *Reader) Run(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}

	defer close(r.snaps)

	r.restore(ctx)

	for {
		r.round(ctx)

		if ctx.Err() != nil {
			return nil
		}

		err := r.sched.Wait(ctx, r.next())
		if err != nil {
			return nil
		}
	}
}

// next is the wait before the next round: the interval spread by the jitter.
func (r *Reader) next() time.Duration {
	wait := r.cfg.PollInterval
	if r.cfg.JitterFraction > 0 {
		factor := 1 + r.cfg.JitterFraction*(2*r.jitter()-1)
		wait = time.Duration(float64(wait) * factor)
	}

	return wait
}

func (r *Reader) restore(ctx context.Context) {
	if r.hooks.Restore == nil {
		return
	}

	restored, err := r.hooks.Restore()
	if err != nil {
		if !errors.Is(err, ErrNothingRestored) {
			r.log.LogAttrs(ctx, slog.LevelWarn, "manifest reader: restore failed")
		}

		return
	}

	for _, content := range restored.Objects {
		r.cache[controlplane.ChecksumOf(content).Value] = content
	}

	r.accept(ctx, Object{Bytes: restored.Manifest, ETag: restored.ETag})
	r.finish(ctx, PollStatus{Result: PollNone}, false)
}

// round is one poll: the conditional GET of the manifest and what follows from the answer.
func (r *Reader) round(ctx context.Context) {
	r.polls++

	obj, err := r.store.Get(ctx, r.cfg.ManifestKey, r.etag, r.cfg.MaxManifestBytes)
	if ctx.Err() != nil {
		return
	}

	poll := PollStatus{Result: PollUpdated}
	confirmed := false

	switch {
	case err == nil:
		confirmed = r.accept(ctx, obj)
	case errors.Is(err, ErrNotModified):
		r.notMod++
		poll.Result = PollNotModified
		confirmed = r.manifest.Outcome == OutcomeAccepted

		// only the objects that failed are fetched again; with none, nothing else happens
		if len(r.failures) > 0 {
			r.settle(ctx)
		}
	default:
		poll = failedPoll(err)
	}

	r.logPoll(ctx, poll)
	r.finish(ctx, poll, confirmed)
}

func failedPoll(err error) PollStatus {
	switch {
	case errors.Is(err, ErrNotFound):
		return PollStatus{Result: PollNotFound, Reason: ReasonMissing}
	case errors.Is(err, ErrTooLarge):
		return PollStatus{Result: PollTooLarge, Reason: ReasonTooLarge}
	}

	fetchErr, ok := errors.AsType[*FetchError](err)
	if ok {
		return PollStatus{Result: PollError, Reason: fetchErr.Reason}
	}

	return PollStatus{Result: PollError, Reason: ReasonError}
}

// logPoll logs a failed poll once per change of its reason, and the recovery.
func (r *Reader) logPoll(ctx context.Context, poll PollStatus) {
	if poll.Reason == "" {
		if r.lastPoll != "" {
			r.lastPoll = ""
			r.log.LogAttrs(
				ctx,
				slog.LevelInfo,
				"manifest reader: manifest poll recovered",
				slog.String("result", poll.Result),
			)
		}

		return
	}

	key := poll.Result + "/" + poll.Reason
	if key == r.lastPoll {
		return
	}

	r.lastPoll = key
	r.log.LogAttrs(ctx, slog.LevelWarn, "manifest reader: manifest poll failed",
		slog.String("result", poll.Result), slog.String("reason", poll.Reason))
}

// reject records a manifest that is not applied. The ETag of the rejected bytes is kept on purpose: the same
// bytes are rejected again with the same result, so the next 304 spares the download and the repeated log,
// and a corrected manifest has other bytes, so another ETag, and is read.
func (r *Reader) reject(ctx context.Context, outcome, reason string, unknown int) {
	r.manifest.Outcome = outcome
	r.manifest.Reason = reason
	r.manifest.UnknownFields = unknown

	r.log.LogAttrs(ctx, slog.LevelWarn, "manifest reader: manifest rejected, previous one kept",
		slog.String("outcome", outcome), slog.String("reason", reason),
		slog.Uint64("heldGeneration", r.manifest.Generation))
}

// accept decodes, validates and applies the manifest of a 200 answer. It reports whether the manifest was
// accepted.
func (r *Reader) accept(ctx context.Context, obj Object) bool {
	r.etag = obj.ETag

	manifest, info, err := controlplane.UnmarshalManifest(obj.Bytes)
	if err == nil {
		err = controlplane.ValidateManifest(manifest, controlplane.WithMaxObjects(r.cfg.MaxObjects))
	}

	unknown := len(info.UnknownFields)

	if err != nil {
		r.reject(ctx, OutcomeInvalid, manifestReason(err), unknown)

		return false
	}

	if r.cfg.Channel != "" && manifest.Channel != r.cfg.Channel {
		r.reject(ctx, OutcomeChannelMismatch, "channel", unknown)

		return false
	}

	sums := make(map[string]string, len(manifest.Objects))
	for i := range manifest.Objects {
		sums[manifest.Objects[i].Name] = manifest.Objects[i].Checksum.Value
	}

	if r.held != nil {
		switch {
		case manifest.Generation < r.held.generation:
			r.reject(ctx, OutcomeLowerGeneration, "generation", unknown)

			return false
		case manifest.Generation == r.held.generation && !sameSums(r.held.sums, sums):
			r.reject(ctx, OutcomeConflict, "generation", unknown)

			return false
		}
	}

	r.adopt(manifest, sums, obj.Bytes)
	r.manifest = ManifestStatus{
		Outcome: OutcomeAccepted, Channel: manifest.Channel, Generation: manifest.Generation,
		Objects: len(manifest.Objects), UnknownFields: unknown,
	}

	r.settle(ctx)

	return true
}

func sameSums(have, want map[string]string) bool {
	if len(have) != len(want) {
		return false
	}

	for name, sum := range want {
		if have[name] != sum {
			return false
		}
	}

	return true
}

// manifestReason is the closed code of a manifest that cannot be used. The text of the error is not used: it
// may name fields and objects.
func manifestReason(err error) string {
	valErr, ok := errors.AsType[*controlplane.ValidationError](err)
	if ok && len(valErr.Problems) > 0 {
		return string(valErr.Problems[0].Code)
	}

	switch {
	case errors.Is(err, controlplane.ErrUnsupportedAPIVersion):
		return "unsupported-version"
	case errors.Is(err, controlplane.ErrWrongKind):
		return "wrong-kind"
	case errors.Is(err, controlplane.ErrDecode):
		return "decode"
	}

	return "invalid"
}

// adopt makes manifest the held one. A name that stays keeps its last verified content; a name that left is
// forgotten with it (removal is not a failure).
func (r *Reader) adopt(manifest *controlplane.Manifest, sums map[string]string, raw []byte) {
	names := make(map[string]*nameState, len(manifest.Objects))

	for i := range manifest.Objects {
		entry := manifest.Objects[i]
		state := &nameState{entry: entry, state: StatePending}

		if prev := r.names[entry.Name]; prev != nil {
			state.good = prev.good
		}

		names[entry.Name] = state
	}

	r.names = names
	r.held = &held{
		generation: manifest.Generation, channel: manifest.Channel, sums: sums, raw: raw,
		objects: manifest.Objects,
	}
}

// job is the download of one distinct checksum.
type job struct {
	entry *controlplane.ManifestObject
}

// result is the outcome of a job.
type result struct {
	reason    string
	data      []byte
	retryable bool
}

// settle brings every name of the held manifest to a state: a held verified content, or a download of the
// checksums that are not held, each once. It publishes a snapshot when the generation or any state changed.
func (r *Reader) settle(ctx context.Context) {
	if r.held == nil {
		return
	}

	var jobs []job

	queued := map[string]struct{}{}

	for i := range r.held.objects {
		obj := &r.held.objects[i]
		if _, held := r.cache[obj.Checksum.Value]; held {
			continue
		}

		if _, dup := queued[obj.Checksum.Value]; dup {
			continue
		}

		queued[obj.Checksum.Value] = struct{}{}

		jobs = append(jobs, job{entry: obj})
	}

	results := r.fetchAll(ctx, jobs)
	if ctx.Err() != nil {
		return
	}

	failed := make(map[string]result, len(jobs))

	for i := range jobs {
		res := results[i]
		if res.reason == "" {
			r.cache[jobs[i].entry.Checksum.Value] = res.data

			continue
		}

		failed[jobs[i].entry.Checksum.Value] = res
	}

	for i := range r.held.objects {
		obj := &r.held.objects[i]
		r.apply(r.names[obj.Name], failed[obj.Checksum.Value])
	}

	r.prune()
	r.report(ctx)
	r.publish(ctx)
}

// apply sets the state of one name from the download of its checksum (a zero result when it is held).
func (r *Reader) apply(name *nameState, fail result) {
	entry := name.entry

	if data, ok := r.cache[entry.Checksum.Value]; ok {
		if name.good == nil || name.good.sum.Value != entry.Checksum.Value ||
			name.good.kind != entry.Kind || name.good.format != entry.Format {
			name.good = &good{kind: entry.Kind, format: entry.Format, sum: entry.Checksum, bytes: data}
		}

		name.state, name.reason, name.retryable = StateOK, "", false

		return
	}

	name.reason, name.retryable = fail.reason, fail.retryable
	name.state = StateMissing

	if name.good != nil {
		name.state = StateStale
	}
}

// prune drops the cached bytes that no entry of the held manifest and no stale name refers to.
func (r *Reader) prune() {
	keep := make(map[string]struct{}, len(r.names))

	for _, name := range r.names {
		keep[name.entry.Checksum.Value] = struct{}{}

		if name.good != nil {
			keep[name.good.sum.Value] = struct{}{}
		}
	}

	for sum := range r.cache {
		if _, ok := keep[sum]; !ok {
			delete(r.cache, sum)
		}
	}
}

// report refreshes the counts and failures and logs the state changes of the objects, once per change.
func (r *Reader) report(ctx context.Context) {
	r.counts = Counts{}
	r.failures = r.failures[:0:0]

	for name, state := range r.names {
		switch state.state {
		case StateOK:
			r.counts.OK++
		case StateStale:
			r.counts.Stale++
		case StateMissing:
			r.counts.Missing++
		case StatePending:
			r.counts.Pending++
		}

		if state.state == StateOK {
			if _, was := r.logged[name]; was {
				delete(r.logged, name)
				r.log.LogAttrs(ctx, slog.LevelInfo, "manifest reader: object recovered", slog.String("object", name))
			}

			continue
		}

		r.failures = append(r.failures, ObjectFailure{
			Name: name, State: state.state, Reason: state.reason, Checksum: state.entry.Checksum.Value,
			Retryable: state.retryable,
		})

		if r.logged[name] != state.reason {
			r.logged[name] = state.reason
			r.log.LogAttrs(ctx, slog.LevelWarn, "manifest reader: object not updated",
				slog.String("object", name), slog.String("state", string(state.state)),
				slog.String("reason", state.reason))
		}
	}

	for name := range r.logged {
		if _, ok := r.names[name]; !ok {
			delete(r.logged, name)
		}
	}

	slices.SortFunc(r.failures, func(a, b ObjectFailure) int { return cmp.Compare(a.Name, b.Name) })
}

// publish sends a snapshot when the generation or the state of any object differs from the last one sent.
func (r *Reader) publish(ctx context.Context) {
	snap, keys := r.build()
	if snap == nil {
		return
	}

	r.last, r.lastGen = keys, snap.Generation

	r.send(ctx, snap)

	if r.hooks.Save != nil {
		r.hooks.Save(Persist{Snapshot: snap, ETag: r.etag, Manifest: r.held.raw})
	}
}

// build returns the snapshot of the current states with the keys that identify it, or nil when it does not
// differ from the last one sent.
func (r *Reader) build() (*Snapshot, map[string]pubKey) {
	keys := make(map[string]pubKey, len(r.names))
	changed := r.last == nil || r.lastGen != r.held.generation || len(r.last) != len(r.names)
	objects := make(map[string]ObjectState, len(r.names))

	for name, state := range r.names {
		key := pubKey{state: state.state, reason: state.reason, sum: state.entry.Checksum.Value}
		if state.good != nil {
			key.sum = state.good.sum.Value
		}

		keys[name] = key
		objects[name] = state.objectState()
		changed = changed || r.last[name] != key
	}

	if !changed {
		return nil, nil
	}

	return &Snapshot{Generation: r.held.generation, Channel: r.held.channel, Objects: objects}, keys
}

// send puts snap in the channel, replacing a snapshot that was not consumed. The poller is the only sender:
// after the drain the send cannot find the buffer full.
func (r *Reader) send(ctx context.Context, snap *Snapshot) {
	for {
		select {
		case r.snaps <- snap:
			r.log.LogAttrs(ctx, slog.LevelInfo, "manifest reader: snapshot published",
				slog.Uint64("generation", snap.Generation), slog.Int("objects", len(snap.Objects)),
				slog.Int("notOK", len(r.failures)))

			return
		default:
			select {
			case <-r.snaps:
			default:
			}
		}
	}
}

func (n *nameState) objectState() ObjectState {
	if n.good == nil {
		return ObjectState{
			Kind: n.entry.Kind, Format: n.entry.Format, Checksum: n.entry.Checksum, State: n.state, Reason: n.reason,
		}
	}

	// a stale object carries the kind, format and checksum of the content it carries
	return ObjectState{
		Kind: n.good.kind, Format: n.good.format, Checksum: n.good.sum, State: n.state, Reason: n.reason,
		Bytes: n.good.bytes,
	}
}

// finish stores the status at the end of a round.
func (r *Reader) finish(_ context.Context, poll PollStatus, confirmed bool) {
	now := clock.Now()
	poll.At = now

	if confirmed {
		r.lastSync = now
	}

	status := &Status{
		LastSync: r.lastSync, Poll: poll, Manifest: r.manifest, Failures: r.failures, Counts: r.counts,
		Polls: r.polls, NotModified: r.notMod,
	}
	r.status.Store(status)

	if r.hooks.OnRound != nil {
		r.hooks.OnRound(status)
	}
}

// fetchAll downloads the jobs with at most FetchConcurrency requests in flight and returns the results in
// the order of the jobs. It returns after every worker ended.
func (r *Reader) fetchAll(ctx context.Context, jobs []job) []result {
	results := make([]result, len(jobs))
	if len(jobs) == 0 {
		return results
	}

	next := make(chan int)

	var group sync.WaitGroup

	for range min(r.cfg.FetchConcurrency, len(jobs)) {
		group.Go(func() {
			for idx := range next {
				results[idx] = r.fetch(ctx, jobs[idx].entry)
			}
		})
	}

feed:
	for idx := range jobs {
		select {
		case next <- idx:
		case <-ctx.Done():
			break feed
		}
	}

	close(next)
	group.Wait()

	return results
}

// fetch downloads and verifies one object.
func (r *Reader) fetch(ctx context.Context, entry *controlplane.ManifestObject) result {
	if entry.Size > r.cfg.MaxObjectBytes {
		return result{reason: ReasonTooLarge}
	}

	store := r.store

	if r.stores != nil {
		found, ok := r.stores[entry.Connection]
		if !ok {
			return result{reason: ReasonUnknownConnection}
		}

		store = found
	}

	obj, err := store.Get(ctx, entry.Key, "", entry.Size)

	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		return result{reason: ReasonMissing, retryable: true}
	case errors.Is(err, ErrTooLarge):
		return result{reason: ReasonTooLarge}
	default:
		fetchErr, ok := errors.AsType[*FetchError](err)
		if ok {
			return result{reason: fetchErr.Reason, retryable: fetchErr.Retryable}
		}

		return result{reason: ReasonError, retryable: true}
	}

	if controlplane.VerifyChecksum(obj.Bytes, entry.Checksum) != nil {
		return result{reason: ReasonChecksumMismatch, retryable: true}
	}

	return result{data: obj.Bytes}
}
