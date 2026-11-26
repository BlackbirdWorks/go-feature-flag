package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync/atomic"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// DefaultChangeBuffer is the default size of the change channel.
const DefaultChangeBuffer = 64

// Hooks are injection points for tests and for the wiring PR. A nil hook is not called. Hooks run on the
// engine goroutine: a blocking hook blocks the engine.
type Hooks struct {
	// OnParse is called for every object parsed.
	OnParse func(ParseEvent)
	// BeforeSwap is called with the candidate just before the store. An error refuses the candidate: nothing is
	// stored and the round is rejected with [CodeVetoed].
	BeforeSwap func(candidate *Runtime) error
	// AfterRound is called at the end of every round, after the store and the change notifications.
	AfterRound func(Reload)
}

// Option configures an [Engine].
type Option func(*Engine)

// WithChangeBuffer sets the size of the change channel; values below 1 mean 1.
func WithChangeBuffer(size int) Option {
	return func(e *Engine) { e.changeBuf = max(1, size) }
}

// WithHooks sets the hooks.
func WithHooks(hooks Hooks) Option {
	return func(e *Engine) { e.hooks = hooks }
}

// WithSecretOptions sets how the admin keys that are secret references are resolved.
func WithSecretOptions(opts bootstrap.LoadOptions) Option {
	return func(e *Engine) { e.secrets = opts }
}

// WithInitialBootstrap sets the configuration the process started with: the baseline for pendingRestart. Without
// it the first configuration given to [Engine.Reload] is the baseline.
func WithInitialBootstrap(cfg *bootstrap.Config) Option {
	return func(e *Engine) { e.baseline = cfg }
}

// Engine is the reconcile engine. One goroutine ([Engine.Run]) is the only writer of the [Holder]; the other
// methods only hand it work through channels.
//
// A round: take the latest content and the latest bootstrap configuration (a burst is coalesced); validate the
// content (generation, shape); build the candidate off to the side (parse what changed, rebuild the flag sets
// that use it); compose the candidate Runtime with the admin keys and the status; store it with one atomic
// pointer store; then commit the builder (the old parse results are released), compute one change per rebuilt
// flag set and queue it for the consumer. A rejected candidate stores nothing.
//
// Change events: the consumer reads [Engine.Changes], a channel of bounded size. The engine never blocks on
// it: an event that does not fit is kept per flag set in a pending table and merged with later changes of the
// same flag set (the diff goes from the state the consumer last saw to the newest one), and it is sent when the
// consumer reads again. The table holds at most one entry per flag set name, so it is bounded by the number of
// flag sets, and no name is lost. Pending events that the consumer never reads are dropped when Run returns,
// and the channel is closed.
type Engine struct {
	logger   *slog.Logger
	holder   *Holder
	builder  *Builder
	content  chan *Content
	boot     chan *bootstrap.Config
	changes  chan Change
	pending  map[string]*Change
	baseline *bootstrap.Config
	cur      *Runtime
	last     atomic.Pointer[Reload]
	rejected *Reload
	hooks    Hooks
	secrets  bootstrap.LoadOptions
	queue    []string
	// digest identifies the content that runs; with the generation it tells a repeat from a conflict.
	digest    uint64
	changeBuf int
	snapshot  uint64
}

// NewEngine returns an engine that stores into holder. svc validates the flags and owns the compiled-query
// caches; the caller owns it. A nil logger discards.
func NewEngine(logger *slog.Logger, svc *flagdef.Service, holder *Holder, opts ...Option) *Engine {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	eng := &Engine{
		logger:    logger,
		holder:    holder,
		content:   make(chan *Content, 1),
		boot:      make(chan *bootstrap.Config, 1),
		pending:   map[string]*Change{},
		changeBuf: DefaultChangeBuffer,
		cur:       holder.Load(),
	}

	for _, opt := range opts {
		opt(eng)
	}

	eng.changes = make(chan Change, eng.changeBuf)
	eng.builder = NewBuilder(svc, BuilderOptions{OnParse: eng.hooks.OnParse})
	eng.snapshot = eng.cur.Snapshot

	return eng
}

// Submit hands a content to the engine. It never blocks: a content that the engine has not taken yet is
// replaced (latest wins). Submit may be called from any goroutine.
func (e *Engine) Submit(content *Content) {
	for {
		select {
		case e.content <- content:
			return
		default:
		}

		select {
		case <-e.content:
		default:
		}
	}
}

// Reload hands a bootstrap configuration to the engine, with the same latest-wins rule as [Engine.Submit].
func (e *Engine) Reload(cfg *bootstrap.Config) {
	for {
		select {
		case e.boot <- cfg:
			return
		default:
		}

		select {
		case <-e.boot:
		default:
		}
	}
}

// Changes returns the channel of change events; it is closed when Run returns.
func (e *Engine) Changes() <-chan Change { return e.changes }

// Result returns the result of the last round. It is safe to call from any goroutine.
func (e *Engine) Result() Reload {
	if res := e.last.Load(); res != nil {
		return *res
	}

	return Reload{}
}

// Run is the reconcile loop. It returns when ctx is done; it starts no other goroutine, so nothing is left
// behind. Run must be called once.
func (e *Engine) Run(ctx context.Context) {
	defer close(e.changes)

	for {
		var (
			out  chan<- Change
			head Change
		)

		if len(e.queue) > 0 {
			out, head = e.changes, *e.pending[e.queue[0]]
		}

		select {
		case <-ctx.Done():
			return
		case content := <-e.content:
			e.round(ctx, content, e.takeBoot())
		case cfg := <-e.boot:
			e.round(ctx, e.takeContent(), cfg)
		case out <- head:
			delete(e.pending, e.queue[0])
			e.queue = e.queue[1:]
		}
	}
}

func (e *Engine) takeBoot() *bootstrap.Config {
	select {
	case cfg := <-e.boot:
		return cfg
	default:
		return nil
	}
}

func (e *Engine) takeContent() *Content {
	select {
	case content := <-e.content:
		return content
	default:
		return nil
	}
}

type contentPlan struct {
	built  *Built
	res    Reload
	digest uint64
}

type bootPlan struct {
	admin   map[string]struct{}
	pending []string
	res     Reload
	apply   bool
}

// round runs one reconcile round for the inputs it was woken with.
func (e *Engine) round(ctx context.Context, content *Content, cfg *bootstrap.Config) {
	var (
		plan    contentPlan
		boot    bootPlan
		results []Reload
	)

	if content != nil {
		plan = e.planContent(ctx, content)
		if plan.res.Outcome != "" {
			results = append(results, plan.res)
		}
	}

	if cfg != nil {
		boot = e.planBootstrap(cfg)
		if boot.res.Outcome != "" {
			results = append(results, boot.res)
		}
	}

	bootChanged := boot.apply && (!maps.Equal(boot.admin, e.cur.AdminKeys) ||
		!slices.Equal(boot.pending, e.cur.Status.PendingRestart))

	if plan.built == nil && !bootChanged {
		e.finish(ctx, results, nil)

		return
	}

	veto := e.swap(&plan, &boot)
	if veto != nil {
		e.finish(ctx, append(results, *veto), nil)

		return
	}

	if boot.apply && e.baseline == nil {
		e.baseline = cfg
	}

	e.finish(ctx, results, &e.cur.Status.LastReload)
}

// swap assembles the candidate and stores it with one atomic store. It returns the rejection when the
// BeforeSwap hook refused the candidate; nothing is stored then and the builder is not committed.
func (e *Engine) swap(plan *contentPlan, boot *bootPlan) *Reload {
	candidate := e.assemble(plan.built, boot)

	if e.hooks.BeforeSwap != nil {
		err := e.hooks.BeforeSwap(candidate)
		if err != nil {
			rej := Reload{
				Outcome: OutcomeRejected, Code: CodeVetoed, Generation: candidate.Generation,
				Detail: fmt.Sprintf("%s: %s", ErrVetoed, err),
			}
			e.noteRejected(rej)

			return &rej
		}
	}

	// the one store: a reader sees the old Runtime or the new one, never a mix
	e.holder.store(candidate)
	e.cur = candidate
	e.snapshot = candidate.Snapshot

	if plan.built != nil {
		e.builder.Commit(plan.built)
		e.digest = plan.digest
		e.enqueue(plan.built, candidate)
	}

	return nil
}

// assemble builds the candidate Runtime: the built content (or the running one) plus the admin keys and the
// status of the round.
func (e *Engine) assemble(built *Built, boot *bootPlan) *Runtime {
	base := e.cur
	status := *base.Status
	next := &Runtime{
		FlagSets: base.FlagSets, ByKey: base.ByKey, AdminKeys: base.AdminKeys, Generation: base.Generation,
		Snapshot: e.snapshot + 1,
	}

	if built != nil {
		next.FlagSets, next.ByKey, next.Generation = built.Runtime.FlagSets, built.Runtime.ByKey, built.Runtime.Generation
		status.FlagSets, status.Objects = built.Runtime.Status.FlagSets, built.Runtime.Status.Objects
		status.Entries = built.Runtime.Status.Entries
	}

	if boot.apply {
		next.AdminKeys = boot.admin
		status.PendingRestart = boot.pending
	}

	status.Generation, status.Snapshot = next.Generation, next.Snapshot
	status.LastRejected = e.rejected
	status.LastReload = Reload{
		Outcome: OutcomeApplied, Code: CodeApplied, Generation: next.Generation, Snapshot: next.Snapshot,
	}
	next.Status = &status

	return next
}

func (e *Engine) noteRejected(res Reload) {
	e.rejected = &res
}

func (e *Engine) planContent(ctx context.Context, content *Content) contentPlan {
	digest, err := validateContent(content)
	if err != nil {
		return e.reject(content.Generation, CodeInvalidContent, err)
	}

	switch {
	case content.Generation < e.cur.Generation:
		return contentPlan{res: Reload{
			Outcome:    OutcomeIgnored,
			Code:       CodeStaleGeneration,
			Generation: content.Generation,
			Detail: fmt.Sprintf(
				"%s: generation %d is older than %d",
				ErrStaleGeneration,
				content.Generation,
				e.cur.Generation,
			),
		}}
	case content.Generation == e.cur.Generation && digest == e.digest:
		return contentPlan{res: Reload{
			Outcome: OutcomeIgnored, Code: CodeDuplicate, Generation: content.Generation,
			Detail: fmt.Sprintf("%s: generation %d is already running", ErrStaleGeneration, content.Generation),
		}}
	case content.Generation == e.cur.Generation:
		return e.reject(content.Generation, CodeGenerationConflict, fmt.Errorf(
			"%w: generation %d is running with other content", ErrGenerationConflict, content.Generation))
	}

	built, err := e.builder.Build(ctx, content)
	if err != nil {
		return e.reject(content.Generation, CodeBuildFailed, err)
	}

	return contentPlan{built: built, digest: digest}
}

func (e *Engine) reject(generation uint64, code string, err error) contentPlan {
	res := Reload{Outcome: OutcomeRejected, Code: code, Generation: generation, Detail: err.Error()}
	e.noteRejected(res)

	return contentPlan{res: res}
}

func (e *Engine) planBootstrap(cfg *bootstrap.Config) bootPlan {
	admin, err := resolveAdminKeys(cfg, e.secrets)
	if err != nil {
		res := Reload{
			Outcome: OutcomeRejected, Code: CodeInvalidBootstrap, Generation: e.cur.Generation, Detail: err.Error(),
		}
		e.noteRejected(res)

		return bootPlan{res: res}
	}

	base := e.baseline
	if base == nil {
		base = cfg
	}

	return bootPlan{admin: admin, pending: pendingRestart(base, cfg), apply: true}
}

// validateContent checks the shape of a content and returns its digest (order independent).
func validateContent(content *Content) (uint64, error) {
	if content.Generation == 0 {
		return 0, fmt.Errorf("%w: generation must be at least 1", ErrInvalidContent)
	}

	var digest uint64

	for name := range content.Objects {
		obj := content.Objects[name]

		err := validateObject(name, &obj)
		if err != nil {
			return 0, err
		}

		sum := mixString(hashString(name), obj.Kind)
		sum = mixString(sum, obj.Format)
		sum = mixString(sum, string(obj.state()))
		sum = mixString(sum, obj.checksum())
		digest ^= sum
	}

	return digest, nil
}

func validateObject(name string, obj *Object) error {
	if name == "" {
		return fmt.Errorf("%w: an object has an empty name", ErrInvalidContent)
	}

	switch obj.state() {
	case ObjectOK, ObjectStale, ObjectMissing, ObjectPending:
	case ObjectInvalid:
		return fmt.Errorf("%w: object %q: state %q is not an input state", ErrInvalidContent, name, obj.State)
	default:
		return fmt.Errorf("%w: object %q: unknown state %q", ErrInvalidContent, name, obj.State)
	}

	if obj.Kind == KindFlags {
		switch obj.Format {
		case "", "yaml", "yml", "json", "toml":
		default:
			return fmt.Errorf("%w: object %q: unsupported format %q", ErrInvalidContent, name, obj.Format)
		}
	}

	return nil
}

// enqueue computes one change per rebuilt flag set and merges it into the pending table. A rebuilt flag set whose
// flags are all unchanged (only its state changed) produces no event.
func (e *Engine) enqueue(built *Built, next *Runtime) {
	for idx := range built.Rebuilt {
		item := &built.Rebuilt[idx]

		change := Change{
			FlagSet: item.Name, Diff: diffFlagSets(item.Old, item.New), Removed: item.New.State == StateWithdrawn,
			Generation: next.Generation, Snapshot: next.Snapshot,
		}

		if !change.Diff.HasDiff() && !change.Removed {
			continue
		}

		prev, queued := e.pending[item.Name]
		if !queued {
			e.pending[item.Name] = &change
			e.queue = append(e.queue, item.Name)

			continue
		}

		compose(prev, &change)

		if !prev.Diff.HasDiff() && !prev.Removed {
			delete(e.pending, item.Name)

			if at := slices.Index(e.queue, item.Name); at >= 0 {
				e.queue = slices.Delete(e.queue, at, at+1)
			}
		}
	}
}

// finish records and logs the results of a round and calls the hook once.
func (e *Engine) finish(ctx context.Context, results []Reload, applied *Reload) {
	final := Reload{Outcome: OutcomeIgnored, Code: "unchanged", Generation: e.cur.Generation, Snapshot: e.cur.Snapshot}

	for idx := range results {
		e.log(ctx, &results[idx])

		final = results[idx]
	}

	if applied != nil {
		e.log(ctx, applied)

		final = *applied
	}

	e.last.Store(&final)

	if e.hooks.AfterRound != nil {
		e.hooks.AfterRound(final)
	}
}

func (e *Engine) log(ctx context.Context, res *Reload) {
	level := slog.LevelInfo
	if res.Outcome == OutcomeRejected {
		level = slog.LevelWarn
	}

	e.logger.LogAttrs(ctx, level, "runtime reload",
		slog.String("outcome", string(res.Outcome)), slog.String("code", res.Code),
		slog.Uint64("generation", res.Generation), slog.Uint64("snapshot", res.Snapshot),
		slog.String("detail", res.Detail))
}
