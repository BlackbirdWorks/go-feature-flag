package valkeysegment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
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
	client    Client
	observer  Observer
	closer    func() error
	segs      map[string]*segmentConfig
	missing   MissingKey
	timeout   time.Duration
	maxID     int
	closeOnce sync.Once
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

	return prov, nil
}

// Dial creates a go-redis client from the options and returns a Provider that owns it: Close closes the client.
func Dial(redisOpts *redis.UniversalOptions, opts *Options) (*Provider, error) {
	if redisOpts == nil {
		return nil, ErrNilClient
	}

	client := redis.NewUniversalClient(redisOpts)

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

// group returns the group of cfg that has room for one more id: a segment with more than MaxIDsPerCommand ids in a
// call is several commands in the same pipeline.
func (pl *plan) group(cfg *segmentConfig) *group {
	for i := len(pl.groups) - 1; i >= 0; i-- {
		if pl.groups[i].cfg == cfg && len(pl.groups[i].idx) < MaxIDsPerCommand {
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
	if len(lookups) == 0 {
		return nil
	}

	// a state that the caller left in the slice never survives a failure
	for i := range lookups {
		lookups[i].State = segment.Unknown
	}

	if p.closed.Load() {
		return ErrClosed
	}

	started := clock.Now()
	scr, _ := p.plans.Get().(*plan)

	defer func() {
		scr.reset()
		p.plans.Put(scr)
	}()

	obs := Observation{Lookups: len(lookups)}
	err := p.lookup(ctx, lookups, scr, &obs)
	p.record(obs.Failure)

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
	execErr  error
	answered int
	failed   int
	class    Class
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

// cmdErr returns the error of a command, or the pipeline error when the command has no reply.
func (r *result) cmdErr(cmd *redis.Cmd) error {
	if cmd == nil {
		return ErrProtocol
	}

	err := cmd.Err()
	if err != nil {
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

	err := r.cmdErr(grp.exists)
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
	err := r.cmdErr(grp.members)
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
	err := r.cmdErr(step.cmd)
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
func (p *Provider) Init(ctx context.Context) error {
	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	probe := probeKey()
	pipe := p.client.Pipeline()

	type check struct {
		cmd  *redis.Cmd
		name string
	}

	checks := []check{{pipe.Do(callCtx, "ping"), "PING"}}

	var sets, keys bool

	for _, cfg := range p.segs {
		sets = sets || cfg.layout == LayoutSet
		keys = keys || cfg.layout == LayoutKeys
	}

	if sets {
		checks = append(checks, check{pipe.Do(callCtx, "smismember", probe, "x"), "SMISMEMBER"})
	}

	if keys {
		checks = append(checks, check{pipe.Do(callCtx, "exists", probe), "EXISTS"})
	}

	if sets && p.atomicTx && p.missing == MissingUnknown {
		checks = append(checks, check{pipe.Do(callCtx, "eval", "return 1", 0), "EVAL"})
	}

	readOnly := pipe.Do(callCtx, "eval_ro", "return 1", 0)

	_, execErr := pipe.Exec(callCtx)
	res := result{ctx: ctx, callCtx: callCtx, execErr: execErr}

	for _, each := range checks {
		err := res.cmdErr(each.cmd)
		if err == nil {
			continue
		}

		class := res.classify(err)
		p.record(class)

		if class == ClassServer {
			return fmt.Errorf("%w: the command %s is not available: %w", ErrInit, each.name, ErrServer)
		}

		return fmt.Errorf("%w: %w", ErrInit, (&LookupError{Class: class}).Unwrap())
	}

	p.roScript.Store(res.cmdErr(readOnly) == nil)
	p.record(ClassNone)

	return nil
}

// probeKey is a key that no segment uses, for the probes of Init.
func probeKey() string {
	return DefaultKeyPrefix + "{init-probe}"
}

// Shutdown closes the client when the provider owns it (see Dial) and marks the provider closed. A second call
// returns nil.
func (p *Provider) Shutdown(_ context.Context) error {
	p.closed.Store(true)

	return p.Close()
}

// Exists tells whether the segments are loaded: Member when the key of a set exists, NotMember when it does not,
// Unknown for a segment of the keys layout (it has no single key) or one that is not configured, or when the
// store could not answer. A failure returns a *LookupError and leaves the states of the answered names.
func (p *Provider) Exists(ctx context.Context, names []string) ([]segment.State, error) {
	out := make([]segment.State, len(names))
	if len(names) == 0 {
		return out, nil
	}

	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	pipe := p.client.Pipeline()
	cmds := make([]*redis.Cmd, len(names))
	asked := 0

	for i, name := range names {
		cfg := p.segs[name]
		if cfg == nil || cfg.layout != LayoutSet {
			continue
		}

		cmds[i] = pipe.Do(callCtx, "exists", cfg.keyAny)
		asked++
	}

	if asked == 0 {
		return out, nil
	}

	_, execErr := pipe.Exec(callCtx)
	res := result{ctx: ctx, callCtx: callCtx, execErr: execErr}

	for i, cmd := range cmds {
		if cmd == nil {
			continue
		}

		err := res.cmdErr(cmd)
		if err != nil {
			res.fail(1, res.classify(err))

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

		return out, &LookupError{Class: res.class, Failed: res.failed, Total: len(names)}
	}

	return out, nil
}

func (p *Provider) lookup(ctx context.Context, lookups []segment.Lookup, scr *plan, obs *Observation) error {
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

	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	pipe := p.client.Pipeline()
	p.queue(callCtx, pipe, lookups, scr)

	_, execErr := pipe.Exec(callCtx)
	obs.RoundTrips = 1

	if p.loadScripts(callCtx, lookups, scr) {
		obs.RoundTrips = 2
	}

	var res result

	res.ctx, res.callCtx, res.execErr = ctx, callCtx, execErr

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
			g := scr.group(cfg)
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
// or flushed. It returns true when it sent the pipeline.
func (p *Provider) loadScripts(ctx context.Context, lookups []segment.Lookup, scr *plan) bool {
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
		return false
	}

	_, _ = pipe.Exec(ctx) // the error of each command is on the command

	return true
}
