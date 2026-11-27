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

	"github.com/redis/go-redis/v9"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// Client is the part of a go-redis client that the provider uses. A [redis.UniversalClient] (a single node, a
// failover set or a cluster) satisfies it.
type Client interface {
	Pipeline() redis.Pipeliner
}

type segmentConfig struct {
	keyAny any // the key of the set, or the prefix of the member keys, boxed once
	key    string
	layout Layout
}

// Provider is a [segment.Provider] over Valkey. It is safe for concurrent use.
type Provider struct {
	plans     sync.Pool
	stopCtx   context.Context //nolint:containedctx // ends the calls in flight when Shutdown runs out of time
	stopStore context.CancelFunc
	client    Client
	observer  Observer
	closer    func() error
	segs      map[string]*segmentConfig
	drained   chan struct{}
	missing   MissingKey
	probeSets []string // keys of the set segments that Init probes: one per namespace, see probeKeys
	probeKeys []string // member keys that Init probes for the keys layout, one per namespace
	timeout   time.Duration
	maxID     int
	inflight  sync.WaitGroup
	admitMu   sync.Mutex // orders the admission of a call (check closed, then Add) against Shutdown
	closeOnce sync.Once
	drainOnce sync.Once
	lastFail  atomic.Uint32
	ready     atomic.Bool
	closed    atomic.Bool
	roScript  atomic.Bool
	atomicTx  bool
}

var (
	_ segment.Provider       = (*Provider)(nil)
	_ segment.Initializer    = (*Provider)(nil)
	_ segment.Shutdowner     = (*Provider)(nil)
	_ segment.Existencer     = (*Provider)(nil)
	_ segment.HealthReporter = (*Provider)(nil)
)

// contextTimeout is implemented by the clients of go-redis that expose their options.
type (
	nodeOptions    interface{ Options() *redis.Options }
	clusterOptions interface{ Options() *redis.ClusterOptions }
)

// checkContextTimeout rejects a go-redis client that ignores the deadline of the context: it would keep a call
// waiting on the socket past the timeout of the provider. That is a client without ContextTimeoutEnabled, and one
// whose read or write timeout is negative: go-redis sets no socket deadline for it. A node client normalises the
// -2 of a caller to -1, a cluster client keeps the -2 as set, and both read a -1 as the default, so the rule is on
// the value that the client reports. A client that does not expose its options (a test double) is accepted.
func checkContextTimeout(client Client) error {
	enabled, read, write := true, time.Second, time.Second

	switch typed := client.(type) {
	case nodeOptions:
		opts := typed.Options()
		enabled, read, write = opts.ContextTimeoutEnabled, opts.ReadTimeout, opts.WriteTimeout
	case clusterOptions:
		opts := typed.Options()
		enabled, read, write = opts.ContextTimeoutEnabled, opts.ReadTimeout, opts.WriteTimeout
	}

	if !enabled {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, ErrContextTimeoutDisabled)
	}

	if read < 0 || write < 0 {
		return fmt.Errorf("%w: %w", ErrInvalidOptions, ErrSocketDeadlineDisabled)
	}

	return nil
}

// MaxInitProbes is the most namespaces that Init probes, the set and the keys layouts together: a provider with
// segments in more namespaces checks the first MaxInitProbes of them by name, so startup stays one small pipeline.
const MaxInitProbes = 16

// probeKeys returns the keys that Init probes. A namespace is the key of a set up to its hash tag, or the prefix of
// the member keys. Each distinct namespace is probed once, with the key of its first segment by name, and at most
// MaxInitProbes namespaces are probed (the first ones by namespace and name), sets and keys layouts each.
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

		namespace, probe := cfg.key, cfg.key
		list := &sets

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

// New returns a Provider that reads through client. The caller owns the client and closes it. A go-redis client
// must have ContextTimeoutEnabled set (Dial does it): New fails with ErrContextTimeoutDisabled when the client
// exposes its options and it is not.
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

	err = checkContextTimeout(client)
	if err != nil {
		return nil, err
	}

	prefix := opts.KeyPrefix
	if prefix == "" {
		prefix = DefaultKeyPrefix
	}

	prov := &Provider{
		client:   client,
		segs:     make(map[string]*segmentConfig, len(opts.Segments)),
		observer: opts.Observer,
		timeout:  opts.Timeout,
		missing:  opts.MissingKey,
		atomicTx: opts.Atomic,
		maxID:    opts.MaxIDBytes,
	}
	if prov.maxID == 0 {
		prov.maxID = DefaultMaxIDBytes
	}

	if prov.timeout == 0 {
		prov.timeout = DefaultTimeout
	}

	if prov.missing == "" {
		prov.missing = MissingUnknown
	}

	prov.plans.New = func() any { return &plan{} }
	prov.stopCtx, prov.stopStore = context.WithCancel(context.Background())

	for name, so := range opts.Segments {
		cfg := &segmentConfig{layout: so.Layout, key: so.Key}
		if cfg.layout == "" {
			cfg.layout = LayoutSet
		}

		if cfg.key == "" {
			if cfg.layout == LayoutSet {
				cfg.key = prefix + "{" + name + "}"
			} else {
				cfg.key = prefix + name + ":"
			}
		}

		cfg.keyAny = cfg.key
		prov.segs[name] = cfg
	}

	prov.probeSets, prov.probeKeys = probeKeys(prov.segs)

	return prov, nil
}

// Dial creates a go-redis client from the options and returns a Provider that owns it: Close closes the client.
func Dial(redisOpts *redis.UniversalOptions, opts *Options) (*Provider, error) {
	if redisOpts == nil {
		return nil, ErrNilClient
	}

	owned := *redisOpts
	// without it go-redis reads the socket under its own timeouts and not under the deadline of the call
	owned.ContextTimeoutEnabled = true

	client := redis.NewUniversalClient(&owned)

	prov, err := New(client, opts)
	if err != nil {
		_ = client.Close()

		return nil, err
	}

	prov.closer = client.Close

	return prov, nil
}

// Close closes the client when the provider owns it (see Dial) and does nothing otherwise. It is safe to call more
// than once: the first call returns the error of the client, the next ones return nil.
func (p *Provider) Close() error {
	var err error

	p.closeOnce.Do(func() {
		if p.closer != nil {
			err = p.closer()
		}
	})

	return err
}

// group is the lookups of one set segment in one call.
type group struct {
	cfg      *segmentConfig
	members  *redis.Cmd
	exists   *redis.Cmd
	idx      []int
	scripted bool
}

// keyStep is one lookup of a keys segment.
type keyStep struct {
	cmd *redis.Cmd
	idx int
}

// plan is the scratch of one call. It is pooled, and it holds no string after the call.
type plan struct {
	groups []group
	keys   []keyStep
	args   []any
}

func (pl *plan) reset() {
	for i := range pl.groups {
		pl.groups[i] = group{idx: pl.groups[i].idx[:0]}
	}

	pl.groups = pl.groups[:0]
	clear(pl.keys)
	pl.keys = pl.keys[:0]
	clear(pl.args)
	pl.args = pl.args[:0]
}

// group returns the group of cfg that has room for one more id under the limit: a segment with more ids in a call
// is several commands in the same pipeline.
func (pl *plan) group(cfg *segmentConfig, limit int) *group {
	for i := len(pl.groups) - 1; i >= 0; i-- {
		if pl.groups[i].cfg == cfg && len(pl.groups[i].idx) < limit {
			return &pl.groups[i]
		}
	}

	if len(pl.groups) < cap(pl.groups) {
		pl.groups = pl.groups[:len(pl.groups)+1]
	} else {
		pl.groups = append(pl.groups, group{})
	}

	g := &pl.groups[len(pl.groups)-1]
	g.cfg = cfg
	g.idx = g.idx[:0]

	return g
}

const (
	// argsPerLookup is the most arguments that one lookup adds to the commands: see queue.
	argsPerLookup = 5
	// argsSlack covers the fixed arguments of the last command.
	argsSlack = 4
)

// take returns the args from start to the end of the arena, with a capacity that stops there.
func (pl *plan) take(start int) []any {
	return pl.args[start:len(pl.args):len(pl.args)]
}

// Contains implements [segment.Provider]. It sends all the lookups in one pipeline and sets the state of each
// lookup that the store answered. See the package documentation for the failure behaviour.
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
	scr, _ := p.plans.Get().(*plan)

	defer func() {
		scr.reset()
		p.plans.Put(scr)
	}()

	obs := Observation{Lookups: len(lookups)}

	err := p.lookup(ctx, lookups, scr, &obs)
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
	ctx       context.Context //nolint:containedctx // the scratch of one call, never stored
	callCtx   context.Context //nolint:containedctx // the scratch of one call, never stored
	execErr   error
	malformed error // the first parse or protocol error of the call: no reply of the call is trusted
	answered  int
	failed    int
	class     Class
}

// isMalformed tells whether err is a parse or protocol error: not a transport, context or connection error and not
// an error reply of the store.
func isMalformed(err error) bool {
	if err == nil {
		return false
	}

	_, isReply := errors.AsType[redis.Error](err)

	return !isReply && !isTransportErr(err)
}

// newResult starts the outcome of a pipeline. An error of Exec that is a parse or protocol error means the stream
// cannot be trusted, and nothing of the call is kept. The unit is the whole call, whatever the client: a cluster
// client has one batch per node and returns the first error, and a standalone one stamps the error on the commands
// that follow. Keeping the call whole makes both behave alike. See also scan.
func newResult(ctx, callCtx context.Context, execErr error) result {
	res := result{ctx: ctx, callCtx: callCtx, execErr: execErr}
	res.note(execErr)

	return res
}

// note keeps the first parse or protocol error that it is given.
func (r *result) note(err error) {
	if r.malformed == nil && isMalformed(err) {
		r.malformed = err
	}
}

// scan looks at the error of every command of the call: Exec returns only the first error of a pipeline, and in a
// cluster an earlier error of another node can hide a parse error.
func (r *result) scan(scr *plan) {
	for i := range scr.groups {
		r.noteCmd(scr.groups[i].members)
		r.noteCmd(scr.groups[i].exists)
	}

	for i := range scr.keys {
		r.noteCmd(scr.keys[i].cmd)
	}
}

func (r *result) noteCmd(cmd *redis.Cmd) {
	if cmd != nil {
		r.note(cmd.Err())
	}
}

func (r *result) fail(n int, class Class) {
	r.failed += n
	r.noteClass(class)
}

// noteClass keeps the most severe class (the lowest value that is not none).
func (r *result) noteClass(class Class) {
	if r.class == ClassNone || class < r.class {
		r.class = class
	}
}

const (
	// shapeNone is the shape of a reply that is never recovered from a failed pipeline: the probes of Init.
	shapeNone = -2
	// shapeInt is the shape of an integer reply that is 0 or 1. A shape of zero or more is a list of that length of
	// such integers.
	shapeInt = -1
)

// validShape tells whether a reply value is the one a command of the shape returns.
func validShape(val any, shape int) bool {
	switch {
	case shape == shapeInt:
		_, ok := readInt(val)

		return ok
	case shape >= 0:
		replies, ok := val.([]any)
		if !ok || len(replies) != shape {
			return false
		}

		for _, reply := range replies {
			if _, valid := readInt(reply); !valid {
				return false
			}
		}

		return true
	}

	return false
}

// isTransportErr tells whether err is the end of the connection or of the context, or a failure to get a
// connection: EOF, a reset, a closed or timed out socket, a dial error, a cancelled or expired context, and the
// sentinels of go-redis v9 for a connection that cannot be had (ErrClosed, ErrPoolExhausted, ErrPoolTimeout: they
// are all the exported errors of the client that are not replies of the store; ErrCrossSlot and ErrNoScript are
// replies). A parse error or any other protocol error is not.
func isTransportErr(err error) bool {
	for _, sentinel := range [...]error{
		io.EOF, io.ErrUnexpectedEOF, net.ErrClosed, context.Canceled, context.DeadlineExceeded,
		syscall.ECONNRESET, syscall.EPIPE,
		redis.ErrClosed, redis.ErrPoolExhausted, redis.ErrPoolTimeout,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}

	_, ok := errors.AsType[net.Error](err)

	return ok
}

// completed tells whether a command that carries an error got its reply before the pipeline failed. go-redis reads
// the replies in order: a command read before a transport error keeps its value and no error, the failing command
// and the ones after it get the error, and at the end of the call go-redis stamps the pipeline error on every
// command that has none, completed ones included. So a reply is kept only when all of these hold: the error of the
// command is the error of the pipeline, that error is a transport or context error (a parse or protocol error
// means the stream cannot be trusted, so nothing of the batch is kept), and the value is exactly what the command
// returns. A command whose own reply failed has a value that is not valid (zero, or a nil list): it is not kept.
func (r *result) completed(cmd *redis.Cmd, err error, shape int) bool {
	return r.execErr != nil && errors.Is(err, r.execErr) && isTransportErr(r.execErr) && validShape(cmd.Val(), shape)
}

// cmdErr returns the error of a command, or the pipeline error when the command has no reply.
func (r *result) cmdErr(cmd *redis.Cmd, shape int) error {
	if r.malformed != nil {
		return fmt.Errorf("valkey segment: pipeline: %w", r.malformed)
	}

	if cmd == nil {
		return ErrProtocol
	}

	err := cmd.Err()
	if err != nil && !r.completed(cmd, err, shape) {
		return fmt.Errorf("valkey segment: reply: %w", err)
	}

	if cmd.Val() == nil {
		if r.execErr != nil {
			return fmt.Errorf("valkey segment: pipeline: %w", r.execErr)
		}

		return ErrProtocol
	}

	return nil
}

func (r *result) classify(err error) Class {
	switch {
	case r.ctx.Err() != nil:
		return ClassCanceled
	case errors.Is(err, ErrProtocol):
		return ClassProtocol
	case errors.Is(err, context.Canceled):
		return ClassCanceled
	case errors.Is(err, context.DeadlineExceeded), r.callCtx.Err() != nil:
		return ClassTimeout
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return ClassTimeout
	}

	if _, ok := errors.AsType[redis.Error](err); ok {
		return ClassServer
	}

	return ClassUnavailable
}

// boolToInt is 1 for true.
func boolToInt(flag bool) int {
	if flag {
		return 1
	}

	return 0
}

// readInt returns the 0 or 1 of an integer reply.
func readInt(reply any) (int64, bool) {
	val, ok := reply.(int64)
	if !ok || val < 0 || val > 1 {
		return 0, false
	}

	return val, true
}

// Script is the Lua script of an atomic call (see Options.Atomic) for one set: it answers the existence of the key,
// then 1 or 0 for each id, in one atomic step on the node that holds the key. It reads one key, so it runs on a
// cluster, and it has no limit on the number of ids as SMISMEMBER through unpack would.
const Script = `local out = {redis.call('EXISTS', KEYS[1])}
for i = 1, #ARGV do out[i + 1] = redis.call('SISMEMBER', KEYS[1], ARGV[i]) end
return out`

// ScriptSHA is the SHA1 of Script, which EVALSHA names. A test pins it to the script.
const ScriptSHA = "4dc3e21a3a7ca911ad25145699dd3279464e74cf"

// keyCheck is what the EXISTS of a set key says.
type keyCheck struct {
	known  bool // false when the key was not checked or the check failed
	exists bool
}

// checkKey reads the EXISTS of the key of a set. With MissingNotMember a missing key is an empty segment, so the
// key counts as checked and existing: a non-member is a non-member whatever the key.
func (r *result) checkKey(grp *group) keyCheck {
	if grp.exists == nil {
		return keyCheck{known: true, exists: true}
	}

	err := r.cmdErr(grp.exists, shapeInt)
	if err != nil {
		r.noteClass(r.classify(err))

		return keyCheck{}
	}

	val, valid := readInt(grp.exists.Val())
	if !valid {
		r.noteClass(ClassProtocol)

		return keyCheck{}
	}

	return keyCheck{known: true, exists: val == 1}
}

func (r *result) readGroup(grp *group, lookups []segment.Lookup) {
	err := r.cmdErr(grp.members, len(grp.idx)+boolToInt(grp.scripted))
	if err != nil {
		r.fail(len(grp.idx), r.classify(err))

		return
	}

	replies, ok := grp.members.Val().([]any)
	want := len(grp.idx)

	if grp.scripted {
		want++ // the first reply of the script is the key check
	}

	if !ok || len(replies) != want {
		r.fail(len(grp.idx), ClassProtocol)

		return
	}

	for _, reply := range replies {
		if _, valid := readInt(reply); !valid {
			r.fail(len(grp.idx), ClassProtocol)

			return
		}
	}

	// A member is certain whatever the key check says. A non-member needs the key to exist.
	var check keyCheck

	if grp.scripted {
		exists, _ := readInt(replies[0])
		check = keyCheck{known: true, exists: exists == 1}
		replies = replies[1:]
	} else {
		check = r.checkKey(grp)
	}

	r.assign(grp, replies, check, lookups)
}

// assign sets the state of the lookups of a group from the replies and the key check.
func (r *result) assign(grp *group, replies []any, check keyCheck, lookups []segment.Lookup) {
	for pos, reply := range replies {
		val, _ := readInt(reply)

		switch {
		case val == 1:
			lookups[grp.idx[pos]].State = segment.Member
			r.answered++
		case check.known && check.exists:
			lookups[grp.idx[pos]].State = segment.NotMember
			r.answered++
		case check.known:
			// the key is missing: the segment is not loaded, the lookup stays unknown
		default:
			r.failed++
		}
	}
}

func (r *result) readKey(step *keyStep, lookups []segment.Lookup) {
	err := r.cmdErr(step.cmd, shapeInt)
	if err != nil {
		r.fail(1, r.classify(err))

		return
	}

	val, ok := readInt(step.cmd.Val())
	if !ok {
		r.fail(1, ClassProtocol)

		return
	}

	if val == 1 {
		lookups[step.idx].State = segment.Member
	} else {
		lookups[step.idx].State = segment.NotMember
	}

	r.answered++
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

// ErrInit is wrapped, with the sentinel of the class, by the error of Init.
var ErrInit = errors.New("valkey segment: init failed")

// Init checks that the store answers (a PING under the timeout of the provider) and that it has the commands that
// the layouts need: SMISMEMBER for a set segment, EXISTS for a keys segment, EVAL for Atomic. It also learns
// whether the read-only script commands exist, and uses them from then on. It fails with ErrInit, naming the
// command that is missing, so an old or restricted server is found at the start and not at the first request. A
// provider needs no Init to work.
func (p *Provider) Init(ctx context.Context) (err error) {
	if !p.admit() {
		return ErrClosed
	}

	defer p.inflight.Done()

	if ctx.Err() != nil {
		p.record(ClassCanceled)

		return fmt.Errorf("%w: %w", ErrInit, ErrCanceled)
	}

	defer func() {
		if recover() != nil {
			p.record(ClassUnavailable)

			err = fmt.Errorf("%w: %w", ErrInit, ErrUnavailable)
		}
	}()

	return p.probe(ctx)
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

	return errors.Join(waitErr, p.Close())
}

// Exists tells whether the segments are loaded: Member when the key of a set exists, NotMember when it does not,
// Unknown for a segment of the keys layout (it has no single key) or one that is not configured, or when the
// store could not answer. A failure returns a *LookupError and leaves the states of the answered names.
func (p *Provider) Exists(ctx context.Context, names []string) ([]segment.State, error) {
	out := make([]segment.State, len(names))

	if !p.admit() {
		return out, ErrClosed
	}

	defer p.inflight.Done()

	if len(names) == 0 {
		return out, nil
	}

	asked := 0

	for _, name := range names {
		if cfg := p.segs[name]; cfg != nil && cfg.layout == LayoutSet {
			asked++
		}
	}

	if asked == 0 {
		return out, nil
	}

	if ctx.Err() != nil {
		p.record(ClassCanceled)

		return out, &LookupError{Class: ClassCanceled, Failed: asked, Total: len(names)}
	}

	err := p.askExists(ctx, names, asked, out)

	return out, err
}

// askExists sends the EXISTS of the set segments of names in one pipeline and fills out. A panic of the client is
// recovered: out is left all Unknown.
func (p *Provider) askExists(ctx context.Context, names []string, asked int, out []segment.State) (err error) {
	defer func() {
		if recover() != nil {
			p.record(ClassUnavailable)

			clear(out)

			err = &LookupError{Class: ClassUnavailable, Failed: asked, Total: len(names)}
		}
	}()

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	pipe := p.client.Pipeline()
	cmds := make([]*redis.Cmd, len(names))

	for i, name := range names {
		cfg := p.segs[name]
		if cfg == nil || cfg.layout != LayoutSet {
			continue
		}

		cmds[i] = pipe.Do(callCtx, "exists", cfg.keyAny)
	}

	_, execErr := pipe.Exec(callCtx)
	res := newResult(ctx, callCtx, execErr)

	for i, cmd := range cmds {
		if cmd == nil {
			continue
		}

		cmdErr := res.cmdErr(cmd, shapeInt)
		if cmdErr != nil {
			res.fail(1, res.classify(cmdErr))

			continue
		}

		val, valid := readInt(cmd.Val())
		if !valid {
			res.fail(1, ClassProtocol)

			continue
		}

		out[i] = segment.NotMember
		if val == 1 {
			out[i] = segment.Member
		}
	}

	if res.failed > 0 {
		p.record(res.class)

		return &LookupError{Class: res.class, Failed: res.failed, Total: len(names)}
	}

	p.record(ClassNone)

	return nil
}

// initCheck is one probe of Init: the command, the name that an error of Init gives, and the shape of its reply.
type initCheck struct {
	cmd   *redis.Cmd
	name  string
	shape int
}

// scriptShape is the reply of the script for one id: the key check, then the answer of the id.
const scriptShape = 2

// probe sends the probes of Init: exactly the commands that the provider will use, on the keys of each configured
// namespace (at most MaxInitProbes of them). A set segment is probed with SMISMEMBER and EXISTS, or with the script
// when Atomic applies (see queue); a keys segment with EXISTS. The keys are the ones the operator configured, so a
// user limited to those namespaces can pass. EXISTS of Provider.Exists is probed only when the lookups need it.
func (p *Provider) probe(ctx context.Context) error {
	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	pipe := p.client.Pipeline()
	ping := pipe.Do(callCtx, "ping")
	checks := []initCheck{{ping, "PING", shapeNone}}
	scripted := p.atomicTx && p.missing == MissingUnknown

	var scripts []initCheck

	for _, key := range p.probeSets {
		switch {
		case scripted:
			scripts = append(scripts,
				initCheck{pipe.Do(callCtx, "eval_ro", Script, 1, key, probeID), "EVAL_RO", scriptShape},
				initCheck{pipe.Do(callCtx, "evalsha_ro", ScriptSHA, 1, key, probeID), "EVALSHA_RO", scriptShape},
			)
		default:
			checks = append(checks, initCheck{pipe.Do(callCtx, "smismember", key, probeID), "SMISMEMBER", 1})

			if p.missing == MissingUnknown {
				checks = append(checks, initCheck{pipe.Do(callCtx, "exists", key), "EXISTS", shapeInt})
			}
		}
	}

	for _, key := range p.probeKeys {
		checks = append(checks, initCheck{pipe.Do(callCtx, "exists", key), "EXISTS", shapeInt})
	}

	_, execErr := pipe.Exec(callCtx)
	res := newResult(ctx, callCtx, execErr)

	err := p.initFailure(&res, checks)
	if err != nil {
		return err
	}

	pong, _ := ping.Val().(string)
	if pong != "PONG" {
		return p.initProtocol()
	}

	if len(scripts) > 0 {
		err = p.probeScripts(&res, scripts)
		if err != nil {
			return err
		}
	}

	p.record(ClassNone)

	return nil
}

// initProtocol is the error of Init for a reply that is not the one of the command.
func (p *Provider) initProtocol() error {
	p.record(ClassProtocol)

	return fmt.Errorf("%w: %w", ErrInit, ErrProtocol)
}

// initFailure returns the error of Init for the first probe that failed or answered something else than its
// command returns, or nil.
func (p *Provider) initFailure(res *result, checks []initCheck) error {
	for _, each := range checks {
		err := res.cmdErr(each.cmd, shapeNone)
		if err == nil {
			if each.shape == shapeNone || validShape(each.cmd.Val(), each.shape) {
				continue
			}

			return p.initProtocol()
		}

		class := res.classify(err)
		p.record(class)

		if class == ClassServer {
			return fmt.Errorf("%w: the command %s is not available: %w", ErrInit, each.name, ErrServer)
		}

		return fmt.Errorf("%w: %w", ErrInit, (&LookupError{Class: class}).Unwrap())
	}

	return nil
}

// probeScripts reads the probes of the read-only script commands. When the store refuses them, it probes the plain
// commands in a second pipeline, and the provider uses those from then on.
func (p *Provider) probeScripts(res *result, readOnly []initCheck) error {
	err := p.initFailure(res, readOnly)
	if err == nil {
		p.roScript.Store(true)

		return nil
	}

	if !errors.Is(err, ErrServer) {
		return err
	}

	pipe := p.client.Pipeline()
	callCtx := res.callCtx // one timeout of Init for both pipelines

	var plain []initCheck

	for _, key := range p.probeSets {
		plain = append(plain,
			initCheck{pipe.Do(callCtx, "eval", Script, 1, key, probeID), "EVAL", scriptShape},
			initCheck{pipe.Do(callCtx, "evalsha", ScriptSHA, 1, key, probeID), "EVALSHA", scriptShape},
		)
	}

	_, execErr := pipe.Exec(callCtx)
	again := newResult(res.ctx, callCtx, execErr)

	err = p.initFailure(&again, plain)
	if err != nil {
		return err
	}

	p.roScript.Store(false)

	return nil
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

// lookup runs the pipeline. A panic of the client is recovered: nothing it left is kept, and every lookup is Unknown.
func (p *Provider) lookup(ctx context.Context, lookups []segment.Lookup, scr *plan, obs *Observation) (err error) {
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

	p.build(lookups, scr, obs)

	if len(scr.groups) == 0 && len(scr.keys) == 0 {
		obs.Unknown = len(lookups)

		return nil
	}

	ctxErr := ctx.Err()
	if ctxErr != nil {
		obs.Unknown = len(lookups)
		obs.Failure = ClassCanceled

		return &LookupError{Class: ClassCanceled, Failed: len(lookups), Total: len(lookups)}
	}

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	pipe := p.client.Pipeline()
	p.queue(callCtx, pipe, lookups, scr)

	_, execErr := pipe.Exec(callCtx)
	obs.RoundTrips = 1

	sent, retryErr := p.loadScripts(callCtx, lookups, scr)
	if sent {
		obs.RoundTrips = 2
	}

	res := newResult(ctx, callCtx, execErr)
	res.note(retryErr)
	res.scan(scr)

	for i := range scr.groups {
		res.readGroup(&scr.groups[i], lookups)
	}

	for i := range scr.keys {
		res.readKey(&scr.keys[i], lookups)
	}

	obs.Answered = res.answered
	obs.Unknown = len(lookups) - res.answered

	if res.failed == 0 {
		return nil
	}

	obs.Failure = res.class

	return &LookupError{Class: res.class, Failed: res.failed, Total: len(lookups)}
}

// build groups the lookups by store command.
func (p *Provider) build(lookups []segment.Lookup, scr *plan, obs *Observation) {
	// SMISMEMBER takes at most MaxIDsPerCommand ids. A script does not unpack them, and it is the unit of atomicity:
	// all the ids of a set go in one script, so a set that is deleted during the call is seen whole or not at all.
	limit := MaxIDsPerCommand
	if p.atomicTx && p.missing == MissingUnknown {
		limit = math.MaxInt
	}

	for i := range lookups {
		cfg := p.segs[lookups[i].Segment]
		if cfg == nil {
			continue
		}

		if len(lookups[i].ID) > p.maxID {
			obs.Oversized++ // never sent: the lookup stays Unknown

			continue
		}

		if cfg.layout == LayoutSet {
			g := scr.group(cfg, limit)
			g.idx = append(g.idx, i)

			continue
		}

		if lookups[i].ID == "" {
			continue // the key would be the bare prefix
		}

		scr.keys = append(scr.keys, keyStep{idx: i})
	}

	if need := argsPerLookup*len(lookups) + argsSlack; cap(scr.args) < need {
		scr.args = make([]any, 0, need)
	}
}

// queue adds the commands of the plan to the pipeline.
func (p *Provider) queue(ctx context.Context, pipe redis.Pipeliner, lookups []segment.Lookup, scr *plan) {
	for i := range scr.groups {
		grp := &scr.groups[i]

		start := len(scr.args)

		if p.atomicTx && p.missing == MissingUnknown {
			// one command that checks the key and the ids together, atomically, and that a cluster routes by its key
			scr.args = append(scr.args, p.scriptCommand(true), ScriptSHA, 1, grp.cfg.keyAny)
			grp.scripted = true
		} else {
			scr.args = append(scr.args, "smismember", grp.cfg.keyAny)
		}

		for _, idx := range grp.idx {
			scr.args = append(scr.args, lookups[idx].ID)
		}

		grp.members = pipe.Do(ctx, scr.take(start)...)

		if p.missing == MissingUnknown && !grp.scripted {
			start = len(scr.args)
			scr.args = append(scr.args, "exists", grp.cfg.keyAny)
			grp.exists = pipe.Do(ctx, scr.take(start)...)
		}
	}

	for i := range scr.keys {
		step := &scr.keys[i]
		look := &lookups[step.idx]

		start := len(scr.args)
		scr.args = append(scr.args, "exists", p.segs[look.Segment].key+look.ID)
		step.cmd = pipe.Do(ctx, scr.take(start)...)
	}
}

// record keeps the outcome of the last call for Status.
func (p *Provider) record(failure Class) {
	p.lastFail.Store(uint32(failure))

	if failure == ClassNone {
		p.ready.Store(true)
	}
}

// scriptCommand is the name of the command that runs the script: by its SHA or with its text, read-only when Init
// found that the store has the read-only commands.
func (p *Provider) scriptCommand(bySHA bool) string {
	switch {
	case bySHA && p.roScript.Load():
		return "evalsha_ro"
	case bySHA:
		return "evalsha"
	case p.roScript.Load():
		return "eval_ro"
	}

	return "eval"
}

// isNoScript tells whether a reply error is the answer of a store that does not have the script in its cache.
func isNoScript(err error) bool {
	if err == nil {
		return false
	}

	_, isRedis := errors.AsType[redis.Error](err)

	return isRedis && strings.HasPrefix(err.Error(), "NOSCRIPT")
}

// loadScripts sends again, with the text of the script, the commands that the store refused with NOSCRIPT, in one
// more pipeline. The store caches the script when it runs it, so this happens once for a store that was restarted
// or flushed. It returns true when it sent the pipeline, and the error of that pipeline.
func (p *Provider) loadScripts(ctx context.Context, lookups []segment.Lookup, scr *plan) (bool, error) {
	var pipe redis.Pipeliner

	for i := range scr.groups {
		grp := &scr.groups[i]
		if !grp.scripted || !isNoScript(grp.members.Err()) {
			continue
		}

		if pipe == nil {
			pipe = p.client.Pipeline()
		}

		start := len(scr.args)
		scr.args = append(scr.args, p.scriptCommand(false), Script, 1, grp.cfg.keyAny)

		for _, idx := range grp.idx {
			scr.args = append(scr.args, lookups[idx].ID)
		}

		grp.members = pipe.Do(ctx, scr.take(start)...)
	}

	if pipe == nil {
		return false, nil
	}

	_, err := pipe.Exec(ctx) // the error of each command is on the command too
	if err != nil {
		return true, fmt.Errorf("valkey segment: script pipeline: %w", err)
	}

	return true, nil
}
