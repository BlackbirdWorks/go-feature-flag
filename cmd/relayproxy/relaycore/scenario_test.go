package relaycore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// storeFixture is one kind of control plane store for the end to end scenario.
type storeFixture struct {
	// env holds the secrets the file refers to.
	env map[string]string
	put func(key string, data []byte)
	// down makes the store unreachable.
	down func()
	// doc is the bootstrap file that points the control plane at the store.
	doc string
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
	rdeps, harness := newDeps(t, opts)

	if deps != nil {
		deps(rdeps)
	}

	harness.start(t, cfg, rdeps)
	harness.settle(t)

	// 1. a cold start serves both flag sets
	harness.awaitGeneration(t, 1)

	got, route := harness.value("key-one")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 1, got)

	got, _ = harness.value("key-two")
	assert.EqualValues(t, 2, got)

	// 2. one changed object is live after one poll, and the flag set of the other object is untouched
	setTwo := harness.core.Holder().Load().FlagSets["two"]
	pub.flags[objA] = flagsYAML(10)
	pub.publish()
	harness.poll(t)
	harness.awaitGeneration(t, 2)

	got, _ = harness.value("key-one")
	assert.EqualValues(t, 10, got)
	assert.Same(t, setTwo, harness.core.Holder().Load().FlagSets["two"])

	// 3. a new flag set with a new key works after one poll
	pub.sets = append(
		pub.sets,
		controlplane.FlagSet{Name: "three", Sources: []string{objB}, APIKeys: []string{"key-three"}},
	)
	pub.publish()
	harness.poll(t)
	harness.awaitGeneration(t, 3)

	got, route = harness.value("key-three")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 2, got)

	// 4. a removed flag set's key is unknown after one poll, the others keep serving
	pub.sets = pub.sets[1:]
	pub.publish()
	harness.poll(t)
	harness.awaitGeneration(t, 4)

	_, route = harness.value("key-one")
	assert.Equal(t, flagruntime.RouteUnknown, route)

	got, _ = harness.value("key-two")
	assert.EqualValues(t, 2, got)

	// 5. with the store gone the core keeps serving, and the status says why
	before := harness.core.Holder().Load()

	store.down()
	harness.poll(t)

	got, _ = harness.value("key-two")
	assert.EqualValues(t, 2, got)
	assert.Equal(t, manifestreader.PollError, harness.core.Status().Source.Reader.Poll.Result)
	assert.Same(t, before, harness.core.Holder().Load(), "no new runtime was stored")

	require.Equal(t, uint64(4), harness.core.Status().Source.LastGeneration)
}
