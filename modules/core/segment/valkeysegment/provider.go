package valkeysegment

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
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
	plans    sync.Pool
	client   Client
	observer Observer
	closer   func() error
	segs     map[string]*segmentConfig
	missing  MissingKey
	timeout  time.Duration
}

var _ segment.Provider = (*Provider)(nil)

// New returns a Provider that reads through client. The caller owns the client and closes it.
func New(client Client, opts Options) (*Provider, error) {
	if client == nil {
		return nil, ErrNilClient
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
func Dial(redisOpts *redis.UniversalOptions, opts Options) (*Provider, error) {
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

// Close closes the client when the provider owns it (see Dial) and does nothing otherwise.
func (p *Provider) Close() error {
	if p.closer == nil {
		return nil
	}

	return p.closer()
}

// group is the lookups of one set segment in one call.
type group struct {
	cfg     *segmentConfig
	members *redis.Cmd
	exists  *redis.Cmd
	idx     []int
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

func (pl *plan) group(cfg *segmentConfig) *group {
	for i := range pl.groups {
		if pl.groups[i].cfg == cfg {
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

	started := clock.Now()
	scr, _ := p.plans.Get().(*plan)

	defer func() {
		scr.reset()
		p.plans.Put(scr)
	}()

	obs := Observation{Lookups: len(lookups)}
	err := p.lookup(ctx, lookups, scr, &obs)

	obs.Duration = clock.Now().Sub(started)

	if p.observer != nil {
		p.observer.Observe(obs)
	}

	return err
}

func (p *Provider) lookup(ctx context.Context, lookups []segment.Lookup, scr *plan, obs *Observation) error {
	p.build(lookups, scr)

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
func (p *Provider) build(lookups []segment.Lookup, scr *plan) {
	for i := range lookups {
		cfg := p.segs[lookups[i].Segment]
		if cfg == nil {
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
		scr.args = append(scr.args, "smismember", grp.cfg.keyAny)

		for _, idx := range grp.idx {
			scr.args = append(scr.args, lookups[idx].ID)
		}

		grp.members = pipe.Do(ctx, scr.take(start)...)

		if p.missing == MissingUnknown {
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
	if !ok || len(replies) != len(grp.idx) {
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
	check := r.checkKey(grp)

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
