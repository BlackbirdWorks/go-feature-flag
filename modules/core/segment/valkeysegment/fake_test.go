package valkeysegment_test

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"

	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// fakeReply is the scripted reply of one command: an error or a value.
type fakeReply struct {
	value any
	err   error
}

// fakeStore is an in-memory stand-in for the client: it answers SMISMEMBER and EXISTS from its data and records the
// commands of every pipeline.
type fakeStore struct {
	sets      map[string]map[string]bool
	keys      map[string]bool
	reply     func(args []any) *fakeReply
	unknown   map[string]bool
	exec      func(ctx context.Context) error
	scripts   map[string]bool
	pipelines [][]string
	completed int // when exec fails, this many commands (from the first) got their reply before the failure
	mu        sync.Mutex
}

func (f *fakeStore) Pipeline() redis.Pipeliner { //nolint:ireturn // the Client interface returns it
	return &fakePipe{store: f}
}

// commands returns the commands of each pipeline, each as "name arg arg".
func (f *fakeStore) commands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([][]string, len(f.pipelines))
	for i := range f.pipelines {
		out[i] = append([]string{}, f.pipelines[i]...)
	}

	return out
}

type fakePipe struct {
	redis.Pipeliner

	store *fakeStore
	cmds  []*redis.Cmd
}

func (p *fakePipe) Do(ctx context.Context, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx, args...)
	p.cmds = append(p.cmds, cmd)

	return cmd
}

func render(args []any) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprint(a)
	}

	return strings.Join(parts, " ")
}

func (p *fakePipe) Exec(ctx context.Context) ([]redis.Cmder, error) {
	store := p.store

	rendered := make([]string, len(p.cmds))
	for i, cmd := range p.cmds {
		rendered[i] = render(cmd.Args())
	}

	store.mu.Lock()
	store.pipelines = append(store.pipelines, rendered)
	store.mu.Unlock()

	if store.exec != nil {
		execErr := store.exec(ctx)
		if execErr != nil {
			// a client reads the replies in order, and at the end stamps the error on every command that has none
			for i, cmd := range p.cmds {
				if i < store.completed {
					store.play(cmd)
				}

				cmd.SetErr(execErr)
			}

			return nil, execErr
		}
	}

	for _, cmd := range p.cmds {
		store.play(cmd)
	}

	return nil, nil
}

// cached tells whether the store has the script of the SHA in its cache.
func (f *fakeStore) cached(sha any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	name, _ := sha.(string)

	return f.scripts[name]
}

// cache keeps the script of an EVAL, as a store does.
func (f *fakeStore) cache(name string, args []any) {
	if name != "eval" && name != cmdEvalRO {
		return
	}

	if text, _ := args[1].(string); text == valkeysegment.Script {
		f.mu.Lock()
		defer f.mu.Unlock()

		if f.scripts == nil {
			f.scripts = map[string]bool{}
		}

		f.scripts[valkeysegment.ScriptSHA] = true
	}
}

// evalAnswer plays Script: the existence of the key, then the answer for each id.
func (f *fakeStore) evalAnswer(args []any) any {
	key, _ := args[3].(string)
	out := []any{int64(0)}

	if _, ok := f.sets[key]; ok {
		out[0] = int64(1)
	}

	for _, id := range args[4:] {
		s, _ := id.(string)
		if f.sets[key][s] {
			out = append(out, int64(1))
		} else {
			out = append(out, int64(0))
		}
	}

	return out
}

func (f *fakeStore) answer(args []any) any {
	name, _ := args[0].(string)
	if len(args) < 2 {
		return "PONG"
	}

	if strings.HasPrefix(name, "eval") {
		if len(args) == 3 {
			return int64(1) // the probe of Init: return 1
		}

		return f.evalAnswer(args)
	}

	key, _ := args[1].(string)

	switch name {
	case cmdSMIsMember:
		set := f.sets[key]
		out := make([]any, 0, len(args)-2)

		for _, id := range args[2:] {
			s, _ := id.(string)
			if set[s] {
				out = append(out, int64(1))
			} else {
				out = append(out, int64(0))
			}
		}

		return out
	case cmdPing:
		return "PONG"
	case cmdExists:
		if _, ok := f.sets[key]; ok || f.keys[key] {
			return int64(1)
		}

		return int64(0)
	}

	return nil
}

// play sets the reply of one command: the scripted one, a refusal, or what the data say.
func (f *fakeStore) play(cmd *redis.Cmd) {
	args := cmd.Args()
	name, _ := args[0].(string)

	scripted := (*fakeReply)(nil)
	if f.reply != nil {
		scripted = f.reply(args)
	}

	switch {
	case scripted == nil && f.unknown[name]:
		cmd.SetErr(serverError{msg: "ERR unknown command '" + name + "'"})
	case scripted == nil && strings.HasPrefix(name, "evalsha") && !f.cached(args[1]):
		cmd.SetErr(serverError{msg: "NOSCRIPT No matching script. Please use EVAL."})
	case scripted == nil:
		f.cache(name, args)
		cmd.SetVal(f.answer(args))
	case scripted.err != nil:
		cmd.SetErr(scripted.err)
	default:
		cmd.SetVal(scripted.value)
	}
}
