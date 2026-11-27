package valkeysegment_test

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
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
	exec      func(ctx context.Context) error
	pipelines [][]string
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
			for _, cmd := range p.cmds {
				cmd.SetErr(execErr)
			}

			return nil, execErr
		}
	}

	for _, cmd := range p.cmds {
		args := cmd.Args()

		scripted := (*fakeReply)(nil)
		if store.reply != nil {
			scripted = store.reply(args)
		}

		switch {
		case scripted == nil:
			cmd.SetVal(store.answer(args))
		case scripted.err != nil:
			cmd.SetErr(scripted.err)
		default:
			cmd.SetVal(scripted.value)
		}
	}

	return nil, nil
}

func (f *fakeStore) answer(args []any) any {
	name, _ := args[0].(string)
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
	case cmdExists:
		if _, ok := f.sets[key]; ok || f.keys[key] {
			return int64(1)
		}

		return int64(0)
	}

	return nil
}
