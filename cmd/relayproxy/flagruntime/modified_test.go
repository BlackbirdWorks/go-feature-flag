package flagruntime_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

// stepClock is a clock that moves one hour at every build, so that a build is told apart from the next one.
type stepClock struct{ builds int }

func (c *stepClock) Now() time.Time {
	c.builds++

	return clocktest.Instant().Add(time.Duration(c.builds) * time.Hour)
}

// ModifiedAt is the time of the build that created the flag set (the pointer): a flag set that a later generation
// carries over keeps it, a rebuilt one has the time of the build that rebuilt it, a flag set that only has its
// entry changed is rebuilt too, and a pending flag set has the time of its build like the others.
func TestFlagSet_ModifiedAt(t *testing.T) {
	t.Parallel()

	clk := &stepClock{}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{Now: clk.Now})
	build := func(gen uint64, in objs) *flagruntime.Runtime {
		built, err := builder.Build(context.Background(), content(gen, in))
		require.NoError(t, err)

		builder.Commit(built)

		return built.Runtime
	}
	objects := func(valueA int) objs {
		return objs{
			"fa": flagsObj(valueA, "f"),
			"fb": flagsObj(2, "g"),
			setsName: setsObj(t,
				fset("a", src("fa"), keyA), fset("b", src("fb"), keyB), fset(pendingSet, src("nope"), "key-p")),
		}
	}

	first := build(1, objects(1))
	at1 := clocktest.Instant().Add(time.Hour)

	for _, name := range []string{"a", "b", pendingSet} {
		assert.Equal(t, at1, first.FlagSets[name].ModifiedAt, name)
	}

	assert.Equal(t, flagruntime.StatePending, first.FlagSets[pendingSet].State)

	second := build(2, objects(10))
	at2 := clocktest.Instant().Add(2 * time.Hour)

	assert.Same(t, first.FlagSets["b"], second.FlagSets["b"])
	assert.Equal(t, at1, second.FlagSets["b"].ModifiedAt, "carried over: the time of the build that made it")
	assert.Equal(t, at1, second.FlagSets[pendingSet].ModifiedAt, "a pending flag set that is carried over keeps it too")
	assert.NotSame(t, first.FlagSets["a"], second.FlagSets["a"])
	assert.Equal(t, at2, second.FlagSets["a"].ModifiedAt, "rebuilt: the time of the rebuild")

	// the entry of b changes (another key): rebuilt, new time
	changed := objects(10)
	changed[setsName] = setsObj(t,
		fset("a", src("fa"), keyA), fset("b", src("fb"), keyB, "key-b2"), fset(pendingSet, src("nope"), "key-p"))

	third := build(3, changed)
	at3 := clocktest.Instant().Add(3 * time.Hour)

	assert.Equal(t, at3, third.FlagSets["b"].ModifiedAt)
	assert.Equal(t, at2, third.FlagSets["a"].ModifiedAt)
}

// An invalid entry that keeps the flags the flag set had, and one that never had any, are rebuilt flag sets: they
// get the time of the build that made them.
func TestFlagSet_ModifiedAtOfFrozenAndPlaceholder(t *testing.T) {
	t.Parallel()

	clk := &stepClock{}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{Now: clk.Now})
	build := func(gen uint64, in objs) *flagruntime.Runtime {
		built, err := builder.Build(context.Background(), content(gen, in))
		require.NoError(t, err)

		builder.Commit(built)

		return built.Runtime
	}

	first := build(1, objs{"fa": flagsObj(1, "f"), setsName: setsObj(t, fset("a", src("fa"), keyA))})
	at1 := clocktest.Instant().Add(time.Hour)
	assert.Equal(t, at1, first.FlagSets["a"].ModifiedAt)

	// the entry now lists a source that the manifest does not have: the flag set keeps its flags (frozen), and a new
	// flag set with the same problem has none (a placeholder)
	second := build(2, objs{
		"fa": flagsObj(1, "f"),
		setsName: setsObj(t,
			fset("a", src("fa", "absent"), keyA), fset("fresh", src("absent"), "key-fresh")),
	})
	at2 := clocktest.Instant().Add(2 * time.Hour)

	assert.Equal(t, flagruntime.StateDegraded, second.FlagSets["a"].State)
	assert.Len(t, second.FlagSets["a"].Flags, 1)
	assert.Equal(t, at2, second.FlagSets["a"].ModifiedAt, "the frozen flag set is a new pointer")
	assert.Equal(t, flagruntime.StatePending, second.FlagSets["fresh"].State)
	assert.Equal(t, at2, second.FlagSets["fresh"].ModifiedAt)
}

// Without a clock of its own the builder stamps the clock of the repository.
func TestFlagSet_ModifiedAtDefaultClock(t *testing.T) {
	t.Parallel()

	set := runtimeOf(t, flagsYAML(1, "f"), "yaml")

	assert.Equal(t, clocktest.Instant(), set.ModifiedAt)
}
