package valkeysegment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// Client is the part of a valkey-go client that the provider uses. A [valkey.Client] (a single node, a sentinel
// set or a cluster) satisfies it. The contract for a client that you pass to [New]: DoMulti returns one result for
// each command, in order, and when the connection fails inside a batch it keeps the results that were read and
// gives the error to the others. valkey-go does that only when it pipelines in the background, so create the client
// with ClientOption.AlwaysPipelining (and not DisableAutoPipelining): on its synchronous path, which it takes for a
// call that is alone on a connection, a failure replaces every result of the batch with the error, and the provider
// then answers the whole batch Unknown (the safe direction, but the answered lookups are lost). [Dial] sets both.
type Client interface {
	B() valkey.Builder
	DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult
}

type segmentConfig struct {
	key        string
	versionKey string
	layout     Layout
	snapshot   bool
}

// Provider is a [segment.Provider] over Valkey (or Redis) through valkey-go. It is safe for concurrent use.
type Provider struct {
	stopCtx   context.Context //nolint:containedctx // ends the calls in flight when Shutdown runs out of time
	stopStore context.CancelFunc
	client    Client
	observer  Observer
	closer    func()
	segs      map[string]*segmentConfig
	drained   chan struct{}
	missing   MissingKey
	probeSets []string // keys of the set segments that Init probes: one per namespace
	probeKeys []string // member keys that Init probes for the keys layout: one per namespace
	timeout   time.Duration
	maxID     int
	inflight  sync.WaitGroup
	admitMu   sync.Mutex // orders the admission of a call (check closed, then Add) against Shutdown
	closeOnce sync.Once
	drainOnce sync.Once
	lastFail  atomic.Uint32
	ready     atomic.Bool
	closed    atomic.Bool
}

var (
	_ segment.Provider       = (*Provider)(nil)
	_ segment.Initializer    = (*Provider)(nil)
	_ segment.Shutdowner     = (*Provider)(nil)
	_ segment.Existencer     = (*Provider)(nil)
	_ segment.HealthReporter = (*Provider)(nil)
)

// New returns a Provider that reads through client. The caller owns the client and closes it.
func New(client Client, opts *Options) (*Provider, error) {
	if client == nil {
		return nil, ErrNilClient
	}

	if opts == nil {
		opts = &Options{}
	}

	err := opts.validate()
	if err != nil {
		return nil, err
	}

	prefix := cmp(opts.KeyPrefix, DefaultKeyPrefix)
	prov := &Provider{
		client:   client,
		segs:     make(map[string]*segmentConfig, len(opts.Segments)),
		observer: opts.Observer,
		timeout:  opts.Timeout,
		missing:  opts.MissingKey,
		maxID:    opts.MaxIDBytes,
	}

	if prov.maxID == 0 {
		prov.maxID = DefaultMaxIDBytes
	}

	if prov.timeout == 0 {
		prov.timeout = DefaultTimeout
	}

	if prov.missing == "" {
		prov.missing = MissingNotMember
	}

	prov.stopCtx, prov.stopStore = context.WithCancel(context.Background())

	for name, so := range opts.Segments {
		cfg := &segmentConfig{layout: so.Layout, key: so.Key, versionKey: so.VersionKey, snapshot: so.Snapshot}
		if cfg.layout == "" {
			cfg.layout = LayoutSet
		}

		if cfg.key == "" {
			if cfg.layout == LayoutSet {
				cfg.key = prefix + "{" + name + "}" // the name is a hash tag: RENAME of a temporary key works
			} else {
				cfg.key = prefix + name + ":"
			}
		}

		prov.segs[name] = cfg
	}

	prov.probeSets, prov.probeKeys = probeKeys(prov.segs)

	return prov, nil
}

// cmp returns val, or def when val is empty.
func cmp(val, def string) string {
	if val == "" {
		return def
	}

	return val
}

// Dial creates a valkey-go client from clientOpt and the client settings of opts (Pipelining, Retry and
// BlockingPipeline; their defaults are the safe ones) and returns a Provider that owns it: Close closes
// the client. The settings override the same fields of clientOpt: see [Options] for what each one trades.
func Dial(clientOpt *valkey.ClientOption, opts *Options) (*Provider, error) {
	if clientOpt == nil {
		return nil, ErrNilClient
	}

	if opts == nil {
		opts = &Options{}
	}

	err := opts.validate()
	if err != nil {
		return nil, err
	}

	err = opts.validateClient()
	if err != nil {
		return nil, err
	}

	owned := buildClientOption(clientOpt, opts)

	client, err := valkey.NewClient(owned)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInit, ErrUnavailable) // the error holds an address: not passed on
	}

	prov, err := New(client, opts)
	if err != nil {
		client.Close()

		return nil, err
	}

	prov.closer = client.Close

	return prov, nil
}

// buildClientOption returns the valkey-go options that Dial uses: clientOpt with the client settings of opts.
func buildClientOption(clientOpt *valkey.ClientOption, opts *Options) valkey.ClientOption {
	owned := *clientOpt
	owned.DisableCache = true // lookups use DoMulti, which is never cached: the resolver caches
	owned.DisableRetry = !opts.Retry
	owned.AlwaysPipelining = opts.Pipelining == "" || opts.Pipelining == PipeliningAlways
	owned.DisableAutoPipelining = false
	owned.BlockingPipeline = opts.BlockingPipeline

	if replicas := owned.SendToReplicas; replicas != nil {
		// a snapshot read (SSCAN, GET of the version key) always goes to the primary: a cursor belongs to one node
		owned.SendToReplicas = func(cmd valkey.Completed) bool { return !isSnapshotCommand(cmd) && replicas(cmd) }
	}

	if owned.BlockingPipeline == 0 {
		owned.BlockingPipeline = math.MaxInt // a batch of any size stays on the shared connection
	}

	return owned
}

// Close closes the client when the provider owns it (see Dial) and does nothing otherwise. It is safe to call
// more than once.
func (p *Provider) Close() {
	p.closeOnce.Do(func() {
		if p.closer != nil {
			p.closer()
		}
	})
}

// probeKeys returns the keys that Init probes: each distinct namespace (the key of a set up to its hash tag, or the
// prefix of the member keys) once, by the first segment name, at most MaxInitProbes in all.
func probeKeys(segs map[string]*segmentConfig) ([]string, []string) {
	names := make([]string, 0, len(segs))
	for name := range segs {
		names = append(names, name)
	}

	slices.Sort(names)

	var sets, members []string

	seen := map[string]bool{}

	for _, name := range names {
		cfg := segs[name]
		namespace, probe, list := cfg.key, cfg.key, &sets

		if cfg.layout == LayoutKeys {
			probe, list = cfg.key+probeID, &members
		} else if tag := strings.IndexByte(cfg.key, '{'); tag >= 0 {
			namespace = cfg.key[:tag]
		}

		id := string(cfg.layout) + "|" + namespace
		if seen[id] || len(sets)+len(members) >= MaxInitProbes {
			continue
		}

		seen[id] = true

		*list = append(*list, probe)
	}

	return sets, members
}

// probeID is the id that the probes of Init look up.
const probeID = "init-probe"

// stepsPerSet is the commands of a set segment: its SMISMEMBER and the EXISTS of its key.
const stepsPerSet = 2

type stepKind uint8

const (
	kindMembers   stepKind = iota // SMISMEMBER of the ids of one set segment
	kindSetExists                 // EXISTS of the key of a set, the command after its kindMembers
	kindKey                       // EXISTS of one member key
)

// step is one command of a call and the lookups it answers.
type step struct {
	cfg  *segmentConfig
	idx  []int
	kind stepKind
}

// Contains implements [segment.Provider]. It sends all the lookups in one DoMulti and sets the state of each lookup
// that the store answered. See the package documentation for the failure behaviour.
func (p *Provider) Contains(ctx context.Context, lookups []segment.Lookup) error {
	// a state that the caller left in the slice never survives a failure
	for i := range lookups {
		lookups[i].State = segment.Unknown
	}

	if !p.admit() {
		return ErrClosed
	}

	defer p.inflight.Done()

	if len(lookups) == 0 {
		return nil
	}

	started := clock.Now()
	obs := Observation{Lookups: len(lookups)}
	err := p.lookup(ctx, lookups, &obs)

	if obs.RoundTrips > 0 || obs.Failure != ClassNone {
		p.record(obs.Failure) // a call that reached no store operation says nothing of the store
	}

	obs.Duration = clock.Now().Sub(started)

	if p.observer != nil {
		p.observer.Observe(obs)
	}

	return err
}

// result accumulates the outcome of the commands of one call.
type result struct {
	ctx      context.Context //nolint:containedctx // the scratch of one call, never stored
	callCtx  context.Context //nolint:containedctx // the scratch of one call, never stored
	answered int
	failed   int
	class    Class
}

// isMalformed tells whether err is a parse or protocol error: not a transport or context error and not an error
// reply of the store.
func isMalformed(err error) bool {
	if err == nil {
		return false
	}

	_, isReply := valkey.IsValkeyErr(err)

	return !isReply && !isTransportErr(err)
}

// isTransportErr tells whether err is the end of the connection or of the context, or a failure to get one.
func isTransportErr(err error) bool {
	for _, sentinel := range [...]error{
		io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, context.Canceled, context.DeadlineExceeded,
		syscall.ECONNRESET, syscall.EPIPE, valkey.ErrClosing, valkey.ErrNoAddr, valkey.ErrNoSlot,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}

	_, ok := errors.AsType[net.Error](err)

	return ok
}

func (r *result) classify(err error) Class {
	switch {
	case r.ctx.Err() != nil:
		return ClassCanceled
	case errors.Is(err, context.Canceled):
		return ClassCanceled
	case errors.Is(err, context.DeadlineExceeded), errors.Is(r.callCtx.Err(), context.DeadlineExceeded):
		return ClassTimeout
	case isMalformed(err):
		return ClassProtocol
	}

	netErr, isNet := errors.AsType[net.Error](err)
	if isNet && netErr.Timeout() {
		return ClassTimeout
	}

	if _, ok := valkey.IsValkeyErr(err); ok {
		return ClassServer
	}

	return ClassUnavailable
}

func (r *result) fail(n int, class Class) {
	r.failed += n
	// keep the most severe class (the lowest value that is not none)
	if r.class == ClassNone || class < r.class {
		r.class = class
	}
}

// read sets the states from the results, in the order of the steps. A malformed reply anywhere in the call means
// the stream of the connection cannot be trusted: no answer of the call is kept. A transport error fails only the
// commands that got no reply (the ones before it were read whole, and so were the ones of a healthy cluster node).
func (r *result) read(lookups []segment.Lookup, steps []step, results []valkey.ValkeyResult) {
	if len(results) != len(steps) {
		r.fail(sent(steps), ClassProtocol)

		return
	}

	for i := range results {
		if isMalformed(results[i].Error()) {
			r.fail(sent(steps), ClassProtocol)

			return
		}
	}

	// a reply of the wrong shape anywhere means that nothing of the call is trusted, as a malformed one does
	for i, each := range steps {
		if !shapeOK(&each, results[i]) {
			r.fail(sent(steps), ClassProtocol)

			return
		}
	}

	for i, each := range steps {
		switch each.kind {
		case kindKey:
			r.readKey(&each, lookups, results[i])
		case kindMembers:
			var exists *valkey.ValkeyResult
			if i > 0 && steps[i-1].kind == kindSetExists {
				exists = &results[i-1]
			}

			r.readMembers(&each, lookups, results[i], exists)
		case kindSetExists:
		}
	}
}

// shapeOK tells whether a reply that is not an error has the shape that its command returns. An error reply or a
// transport error is not a shape problem: it fails its own command only.
func shapeOK(each *step, res valkey.ValkeyResult) bool {
	if res.Error() != nil {
		return true
	}

	if each.kind == kindMembers {
		_, err := parseMembers(res, len(each.idx))

		return err == nil
	}

	_, err := flag(res)

	return err == nil
}

// sent is the number of lookups that a call put on the wire.
func sent(steps []step) int {
	total := 0

	for _, each := range steps {
		if each.kind != kindSetExists {
			total += len(each.idx)
		}
	}

	return total
}

func flag(res valkey.ValkeyResult) (bool, error) {
	val, err := res.ToInt64() // an integer reply only: AsInt64 would also read a bulk string
	if err != nil {
		return false, err //nolint:wrapcheck // valkey.IsValkeyErr type-asserts the error: it must stay unwrapped
	}

	if val != 0 && val != 1 {
		return false, ErrProtocol
	}

	return val == 1, nil
}

// parseMembers reads the reply of an SMISMEMBER of want ids: a list of that many integers, each 0 or 1.
func parseMembers(res valkey.ValkeyResult, want int) ([]int64, error) {
	items, err := res.ToArray()
	if err != nil {
		return nil, err //nolint:wrapcheck // valkey.IsValkeyErr type-asserts the error: it must stay unwrapped
	}

	if len(items) != want {
		return nil, ErrProtocol
	}

	replies := make([]int64, len(items))

	for pos := range items {
		replies[pos], err = items[pos].ToInt64() // an integer only: a null or a string is not a reply
		if err != nil {
			return nil, ErrProtocol
		}

		if replies[pos] != 0 && replies[pos] != 1 {
			return nil, ErrProtocol
		}
	}

	return replies, nil
}

func (r *result) readKey(each *step, lookups []segment.Lookup, res valkey.ValkeyResult) {
	found, err := flag(res)
	if err != nil {
		r.fail(1, r.classify(err))

		return
	}

	lookups[each.idx[0]].State = segment.NotMember
	if found {
		lookups[each.idx[0]].State = segment.Member
	}

	r.answered++
}

func (r *result) readMembers(
	each *step,
	lookups []segment.Lookup,
	res valkey.ValkeyResult,
	exists *valkey.ValkeyResult,
) {
	replies, err := parseMembers(res, len(each.idx))
	if err != nil {
		r.fail(len(each.idx), r.classify(err))

		return
	}

	// A member is certain whatever the key check says. A non-member needs the key to exist.
	known, keyExists := exists == nil, exists == nil

	var keyErr error

	if exists != nil {
		keyExists, keyErr = flag(*exists)
		known = keyErr == nil
	}

	for pos, reply := range replies {
		switch {
		case reply == 1:
			lookups[each.idx[pos]].State = segment.Member
			r.answered++
		case known && keyExists:
			lookups[each.idx[pos]].State = segment.NotMember
			r.answered++
		case known:
			// the key is missing: the segment is not loaded, the lookup stays unknown
		default:
			r.fail(1, r.classify(keyErr))
		}
	}
}

// Status is a snapshot of the health of the provider.
type Status struct {
	// LastFailure is the class of the failure of the last call, ClassNone when it succeeded.
	LastFailure Class
	// Ready is true once Init or a call succeeded.
	Ready bool
	// Closed is true after Shutdown.
	Closed bool
}

// Status returns a snapshot of the health of the provider.
func (p *Provider) Status() Status {
	return Status{
		LastFailure: Class(p.lastFail.Load()), //nolint:gosec // G115: only record stores a Class
		Ready:       p.ready.Load(),
		Closed:      p.closed.Load(),
	}
}

// Health implements [segment.HealthReporter].
func (p *Provider) Health() segment.Health {
	health := segment.Health{Ready: p.ready.Load(), Closed: p.closed.Load()}

	if class := Class(p.lastFail.Load()); class != ClassNone { //nolint:gosec // G115: only record stores a Class
		health.Failure = class.String()
	}

	return health
}

// Shutdown marks the provider closed, so no new call starts, waits for the calls in flight until ctx ends, and then
// closes the client when the provider owns it (see Dial). When ctx ends first it ends the calls in flight and returns
// the error of ctx: no call sends a command after Shutdown returns. A second call waits for the same calls and
// closes nothing again.
func (p *Provider) Shutdown(ctx context.Context) error {
	p.admitMu.Lock()
	p.closed.Store(true)
	p.admitMu.Unlock()

	// one waiter for every Shutdown call, however many of them time out
	p.drainOnce.Do(func() {
		p.drained = make(chan struct{})

		go func() {
			p.inflight.Wait()
			close(p.drained)
		}()
	})

	var waitErr error

	select {
	case <-p.drained:
	case <-ctx.Done():
		waitErr = fmt.Errorf("valkey segment: shutdown: %w", ctx.Err())
	}

	p.stopStore()

	if waitErr != nil {
		// valkey-go can wait for its own timer when it closes a connection of a silent peer: not behind the
		// deadline of the caller. closeOnce makes the close happen once, and it ends by itself.
		go p.Close()
	} else {
		p.Close()
	}

	return waitErr
}

// plan groups the lookups by command: a set segment is one SMISMEMBER (and one EXISTS of its key unless a missing
// key is a non-member) for each MaxIDsPerCommand ids, a keys segment is one EXISTS for each id.
func (p *Provider) plan(lookups []segment.Lookup, obs *Observation) []step {
	var steps []step

	open := map[*segmentConfig]int{} // the last members step of a segment, by index

	for i := range lookups {
		cfg := p.segs[lookups[i].Segment]
		if cfg == nil {
			continue
		}

		if len(lookups[i].ID) > p.maxID {
			obs.Oversized++ // never sent: the lookup stays Unknown

			continue
		}

		if cfg.layout == LayoutKeys {
			if lookups[i].ID != "" { // the key would be the bare prefix
				steps = append(steps, step{cfg: cfg, kind: kindKey, idx: []int{i}})
			}

			continue
		}

		pos, ok := open[cfg]
		if !ok || len(steps[pos].idx) >= MaxIDsPerCommand {
			pos = len(steps)
			open[cfg] = pos
			steps = append(steps, step{cfg: cfg, kind: kindMembers})
		}

		steps[pos].idx = append(steps[pos].idx, i)
	}

	if p.missing != MissingUnknown {
		return steps
	}

	withKeys := make([]step, 0, len(steps)*stepsPerSet)

	// the key check comes first: a set that is created between the two commands is then Unknown or answered by
	// SMISMEMBER, never a NotMember that matches no state of the set
	for _, each := range steps {
		if each.kind == kindMembers {
			withKeys = append(withKeys, step{cfg: each.cfg, kind: kindSetExists, idx: each.idx})
		}

		withKeys = append(withKeys, each)
	}

	return withKeys
}

// commands builds the commands of the steps.
func (p *Provider) commands(lookups []segment.Lookup, steps []step) []valkey.Completed {
	builder := p.client.B()
	cmds := make([]valkey.Completed, len(steps))

	for i, each := range steps {
		switch each.kind {
		case kindMembers:
			ids := make([]string, len(each.idx))
			for pos, idx := range each.idx {
				ids[pos] = lookups[idx].ID
			}

			cmds[i] = builder.Smismember().Key(each.cfg.key).Member(ids...).Build()
		case kindSetExists:
			cmds[i] = builder.Exists().Key(each.cfg.key).Build()
		case kindKey:
			cmds[i] = builder.Exists().Key(each.cfg.key + lookups[each.idx[0]].ID).Build()
		}
	}

	return cmds
}

// lookup runs the call. A panic of the client is recovered: nothing it left is kept, and every lookup is Unknown.
func (p *Provider) lookup(ctx context.Context, lookups []segment.Lookup, obs *Observation) (err error) {
	defer func() {
		if recover() == nil {
			return
		}

		for i := range lookups {
			lookups[i].State = segment.Unknown
		}

		*obs = Observation{Lookups: len(lookups), Unknown: len(lookups), Failure: ClassUnavailable}
		err = &LookupError{Class: ClassUnavailable, Failed: len(lookups), Total: len(lookups)}
	}()

	steps := p.plan(lookups, obs)
	if len(steps) == 0 {
		obs.Unknown = len(lookups)

		return nil
	}

	if ctx.Err() != nil {
		obs.Unknown = len(lookups)
		obs.Failure = ClassCanceled

		return &LookupError{Class: ClassCanceled, Failed: len(lookups), Total: len(lookups)}
	}

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	results := p.client.DoMulti(callCtx, p.commands(lookups, steps)...)
	obs.RoundTrips = 1

	res := result{ctx: ctx, callCtx: callCtx}
	res.read(lookups, steps, results)

	obs.Answered = res.answered
	obs.Unknown = len(lookups) - res.answered

	if res.failed == 0 {
		return nil
	}

	obs.Failure = res.class

	return &LookupError{Class: res.class, Failed: res.failed, Total: len(lookups)}
}

// admit lets a call in unless Shutdown has begun. The check and the Add are one step under the lock, so Shutdown
// never waits on a group that a call joins after the wait began. The caller calls Done on the group when it returns.
func (p *Provider) admit() bool {
	p.admitMu.Lock()
	defer p.admitMu.Unlock()

	if p.closed.Load() {
		return false
	}

	p.inflight.Add(1)

	return true
}

// callContext returns the context of one store call: the context of the caller under the timeout of the provider,
// and ended too when Shutdown gives up waiting.
func (p *Provider) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	stop := context.AfterFunc(p.stopCtx, cancel)

	return callCtx, func() {
		stop()
		cancel()
	}
}

// record keeps the outcome of the last call for Status.
func (p *Provider) record(failure Class) {
	p.lastFail.Store(uint32(failure))

	if failure == ClassNone {
		p.ready.Store(true)
	}
}
