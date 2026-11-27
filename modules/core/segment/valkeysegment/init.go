package valkeysegment

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// Init checks that the store answers (a PING under the timeout of the provider) and that it has the commands that
// the layouts need: SMISMEMBER for a set segment, EXISTS for the others, on the keys of the configured namespaces
// (at most MaxInitProbes of them). It fails with ErrInit, naming the command that is missing, so an old or
// restricted server is found at the start and not at the first request. A provider needs no Init to work.
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

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	builder := p.client.B()
	res := result{ctx: ctx, callCtx: callCtx}

	// PING has no key: a cluster client refuses it in the same DoMulti as commands of keys
	pings := p.client.DoMulti(callCtx, builder.Ping().Build())
	if len(pings) != 1 {
		return p.initFailed(ClassProtocol, "")
	}

	pong, pongErr := pings[0].ToString()
	if pongErr != nil {
		return p.initFailed(res.classify(pongErr), "PING")
	}

	if pong != "PONG" {
		return p.initFailed(ClassProtocol, "")
	}

	cmds, names := p.probeCommands(&builder)

	if len(cmds) > 0 {
		results := p.client.DoMulti(callCtx, cmds...)
		if len(results) != len(cmds) {
			return p.initFailed(ClassProtocol, "")
		}

		for i := range results {
			replyErr := probeReply(results[i], names[i])
			if replyErr != nil {
				return p.initFailed(res.classify(replyErr), names[i])
			}
		}
	}

	p.record(ClassNone)

	return nil
}

// probeReply checks that the reply of a probe has the shape that Contains reads: a list of one 0 or 1 for
// SMISMEMBER, a 0 or 1 for EXISTS.
func probeReply(res valkey.ValkeyResult, name string) error {
	if name == cmdSScan || name == cmdGet {
		err := res.Error()
		if err != nil && !valkey.IsValkeyNil(err) {
			return err //nolint:wrapcheck // classified by the caller
		}

		return nil
	}

	if name == "SMISMEMBER" {
		_, err := parseMembers(res, 1)

		return err
	}

	_, err := flag(res)

	return err
}

// probeCommands returns the commands that Init probes and the name that an error gives for each.
func (p *Provider) probeCommands(builder *valkey.Builder) ([]valkey.Completed, []string) {
	var (
		cmds  []valkey.Completed
		names []string
	)

	for _, key := range p.probeSets {
		cmds = append(cmds, builder.Smismember().Key(key).Member(probeID).Build())
		names = append(names, "SMISMEMBER")

		if p.missing == MissingUnknown {
			cmds = append(cmds, builder.Exists().Key(key).Build())
			names = append(names, "EXISTS")
		}
	}

	for _, key := range p.probeKeys {
		cmds = append(cmds, builder.Exists().Key(key).Build())
		names = append(names, "EXISTS")
	}

	budget := max(MaxInitProbes-len(p.probeSets)-len(p.probeKeys), 0)

	for _, probe := range snapshotProbes(p.segs, budget) {
		if probe.name == cmdSScan {
			cmds = append(cmds, builder.Sscan().Key(probe.key).Cursor(0).Count(1).Build())
		} else {
			cmds = append(cmds, builder.Get().Key(probe.key).Build())
		}

		names = append(names, probe.name)
	}

	return cmds, names
}

// initFailed records the class and returns the error of Init for it. A server error names the command.
func (p *Provider) initFailed(class Class, command string) error {
	p.record(class)

	if class == ClassServer && command != "" {
		return fmt.Errorf("%w: the command %s is not available: %w", ErrInit, command, ErrServer)
	}

	return fmt.Errorf("%w: %w", ErrInit, (&LookupError{Class: class}).Unwrap())
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

	return out, p.askExists(ctx, names, out)
}

// askExists sends the EXISTS of the set segments of names in one DoMulti and fills out. A panic of the client is
// recovered: out is left all Unknown.
func (p *Provider) askExists(ctx context.Context, names []string, out []segment.State) (err error) {
	asked := 0

	for _, name := range names {
		if cfg := p.segs[name]; cfg != nil && cfg.layout == LayoutSet {
			asked++
		}
	}

	if asked == 0 {
		return nil
	}

	if ctx.Err() != nil {
		p.record(ClassCanceled)

		return &LookupError{Class: ClassCanceled, Failed: asked, Total: len(names)}
	}

	defer func() {
		if recover() != nil {
			p.record(ClassUnavailable)

			clear(out)

			err = &LookupError{Class: ClassUnavailable, Failed: asked, Total: len(names)}
		}
	}()

	cmds := make([]valkey.Completed, 0, asked)
	slots := make([]int, 0, asked)
	builder := p.client.B()

	for i, name := range names {
		if cfg := p.segs[name]; cfg != nil && cfg.layout == LayoutSet {
			cmds = append(cmds, builder.Exists().Key(cfg.key).Build())
			slots = append(slots, i)
		}
	}

	callCtx, cancel := p.callContext(ctx)
	defer cancel()

	results := p.client.DoMulti(callCtx, cmds...)
	res := result{ctx: ctx, callCtx: callCtx}

	if len(results) != len(cmds) {
		res.fail(len(cmds), ClassProtocol)
	} else {
		res.readExists(out, slots, results)
	}

	if res.failed > 0 {
		p.record(res.class)

		return &LookupError{Class: res.class, Failed: res.failed, Total: len(names)}
	}

	p.record(ClassNone)

	return nil
}

// readExists fills out from the EXISTS results. A malformed reply anywhere discards every answer of the call.
func (r *result) readExists(out []segment.State, slots []int, results []valkey.ValkeyResult) {
	for i := range results {
		if isMalformed(results[i].Error()) {
			r.fail(len(results), ClassProtocol)

			return
		}
	}

	for i := range results {
		if results[i].Error() == nil {
			_, err := flag(results[i])
			if err != nil { // a reply of the wrong shape: nothing of the call is trusted
				r.fail(len(results), ClassProtocol)

				return
			}
		}
	}

	for i := range results {
		found, err := flag(results[i])
		if err != nil {
			r.fail(1, r.classify(err))

			continue
		}

		out[slots[i]] = segment.NotMember
		if found {
			out[slots[i]] = segment.Member
		}
	}
}

// snapshotProbe is one probe of the snapshot mode: the command name and its key.
type snapshotProbe struct {
	name string
	key  string
}

// snapshotProbes returns the probes of the snapshot mode, each key once, at most budget of them: SSCAN of the set of
// each segment that is configured for the snapshot, GET of its version key. They count inside MaxInitProbes with the
// lookup probes. The client never sends them to a replica (see [Dial]), so they run on the node that the reads use.
func snapshotProbes(segs map[string]*segmentConfig, budget int) []snapshotProbe {
	var probes []snapshotProbe

	seen := map[snapshotProbe]bool{}
	add := func(probe snapshotProbe) {
		if !seen[probe] && len(probes) < budget {
			seen[probe] = true
			probes = append(probes, probe)
		}
	}

	for _, name := range slices.Sorted(maps.Keys(segs)) {
		cfg := segs[name]
		if !cfg.snapshot {
			continue
		}

		if cfg.layout == LayoutSet {
			add(snapshotProbe{name: cmdSScan, key: cfg.key})
		}

		if cfg.versionKey != "" {
			add(snapshotProbe{name: cmdGet, key: cfg.versionKey})
		}
	}

	return probes
}
