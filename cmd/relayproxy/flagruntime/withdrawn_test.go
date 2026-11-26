package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

func namedSets(names ...string) []controlplane.FlagSet {
	out := make([]controlplane.FlagSet, 0, len(names))
	for _, name := range names {
		out = append(out, fset(name, src("f1"), "key-"+name))
	}

	return out
}

func TestWithdrawnListIsBoundedAndNewestKept(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{WithdrawnCap: 3})
	base := objs{"f1": flagsObj(1, "x")}
	all := []string{"s1", "s2", "s3", "s4", "s5", "s6"}

	steps := []struct {
		name string
		sets []string
		want []flagruntime.WithdrawnSet
	}{
		{name: "all present", sets: all, want: nil},
		{
			name: "one removed", sets: []string{"s2", "s3", "s4", "s5", "s6"},
			want: []flagruntime.WithdrawnSet{{Name: "s1", Generation: 2}},
		},
		{
			name: "two more removed in one generation",
			sets: []string{"s4", "s5", "s6"},
			want: []flagruntime.WithdrawnSet{
				{Name: "s1", Generation: 2},
				{Name: "s2", Generation: 3},
				{Name: "s3", Generation: 3},
			},
		},
		{
			name: "the cap drops the oldest",
			sets: []string{"s6"},
			want: []flagruntime.WithdrawnSet{
				{Name: "s3", Generation: 3},
				{Name: "s4", Generation: 4},
				{Name: "s5", Generation: 4},
			},
		},
		{
			name: "a flag set that returns leaves the list", sets: []string{"s4", "s6"},
			want: []flagruntime.WithdrawnSet{{Name: "s3", Generation: 3}, {Name: "s5", Generation: 4}},
		},
	}

	for idx, step := range steps {
		built := buildAndCommit(
			t,
			builder,
			content(uint64(idx+1), base.with(objs{setsName: setsObj(t, namedSets(step.sets...)...)})),
		)

		assert.Len(t, built.Runtime.FlagSets, len(step.sets), step.name)

		if step.want == nil {
			assert.Empty(t, built.Runtime.Status.Withdrawn, step.name)
		} else {
			assert.Equal(t, step.want, built.Runtime.Status.Withdrawn, step.name)
		}
	}
}

func TestWithdrawnSetsEmitARemovedEventAndFreeTheirKeys(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{}, flagruntime.WithWithdrawnCap(2))
	harness.start()

	base := objs{"f1": flagsObj(1, "x")}

	harness.feed(content(1, base.with(objs{setsName: setsObj(t, namedSets("a", "b")...)})))
	harness.nextChange()
	harness.nextChange()

	harness.feed(content(2, base.with(objs{setsName: setsObj(t, namedSets("b")...)})))

	removed := harness.nextChange()

	assert.Equal(t, "a", removed.FlagSet)
	assert.True(t, removed.Removed)

	current := harness.holder.Load()

	_, routed := routedSet(current, "key-a")
	assert.False(t, routed, "the key of a withdrawn flag set is unknown")
	assert.Equal(t, []flagruntime.WithdrawnSet{{Name: "a", Generation: 2}}, current.Status.Withdrawn)

	harness.feed(
		content(3, base.with(objs{setsName: setsObj(t, fset("c", src("f1"), "key-a"), fset("b", src("f1"), "key-b"))})),
	)

	owner, routed := routedSet(harness.holder.Load(), "key-a")
	require.True(t, routed)
	assert.Equal(t, "c", owner.Name, "the freed key goes to the new flag set")
}
