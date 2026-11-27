package relaycore_test

import (
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// storeFixture is one kind of control plane store for the end to end scenario.
type storeFixture struct {
	put func(key string, data []byte)
	// down makes the store unreachable.
	down func()
	// doc is the bootstrap file that points the control plane at the store.
	doc string
	// env holds the secrets the file refers to.
	env map[string]string
}

// runScenario is the path of a publisher to a client through the relay core: objects and a manifest in a store, the
// core polling it, and the evaluator answering for a key. Every change goes live after one poll, without a restart.
func runScenario(t *testing.T, store *storeFixture, deps func(*relaycore.Deps)) {
	t.Helper()

	hookURL, _ := hookServer(t)
	store.env[hookEnv] = hookURL
	opts := secretOptions(store.env)

	pub := twoSets(t, store.put)
	pub.publish()

	cfg := parse(t, store.doc, opts)
	rdeps, rg := newDeps(t, opts)

	if deps != nil {
		deps(rdeps)
	}

	rg.start(t, cfg, rdeps)
	rg.settle(t)

	// 1. a cold start serves both flag sets
	rg.awaitGeneration(t, 1)

	got, route := rg.value("key-one")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 1, got)

	got, _ = rg.value("key-two")
	assert.EqualValues(t, 2, got)

	// 2. one changed object is live after one poll, and the flag set of the other object is untouched
	setTwo := rg.core.Holder().Load().FlagSets["two"]
	pub.flags[objA] = flagsYAML(10)
	pub.publish()
	rg.poll(t)
	rg.awaitGeneration(t, 2)

	got, _ = rg.value("key-one")
	assert.EqualValues(t, 10, got)
	assert.Same(t, setTwo, rg.core.Holder().Load().FlagSets["two"])

	// 3. a new flag set with a new key works after one poll
	pub.sets = append(pub.sets, controlplane.FlagSet{Name: "three", Sources: []string{objB}, APIKeys: []string{"key-three"}})
	pub.publish()
	rg.poll(t)
	rg.awaitGeneration(t, 3)

	got, route = rg.value("key-three")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 2, got)

	// 4. a removed flag set's key is unknown after one poll, the others keep serving
	pub.sets = pub.sets[1:]
	pub.publish()
	rg.poll(t)
	rg.awaitGeneration(t, 4)

	_, route = rg.value("key-one")
	assert.Equal(t, flagruntime.RouteUnknown, route)

	got, _ = rg.value("key-two")
	assert.EqualValues(t, 2, got)

	// 5. with the store gone the core keeps serving, and the status says why
	before := rg.core.Holder().Load()

	store.down()
	rg.poll(t)

	got, _ = rg.value("key-two")
	assert.EqualValues(t, 2, got)
	assert.Equal(t, manifestreader.PollError, rg.core.Status().Source.Reader.Poll.Result)
	assert.Same(t, before, rg.core.Holder().Load(), "no new runtime was stored")

	require.Equal(t, uint64(4), rg.core.Status().Source.LastGeneration)
}
