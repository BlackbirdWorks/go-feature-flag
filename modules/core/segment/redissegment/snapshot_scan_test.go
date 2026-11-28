package redissegment_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

const (
	nodeAddr  = "127.0.0.1:1" // never dialed: the hook answers
	notNumber = "the cursor of a scan reply is not a number"
	notList   = "not a cursor and a list of strings"
	cmdSScan  = "sscan"
)

// primaryHook is the hook of a node client: it plays the commands from the fake store and counts the pipelines.
type primaryHook struct {
	store     *fakeStore
	pipelines *atomic.Int64
}

func (primaryHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (primaryHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook { return next }

func (h primaryHook) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		h.pipelines.Add(1)

		for _, cmd := range cmds {
			if plain, ok := cmd.(*redis.Cmd); ok {
				h.store.play(plain)
			}
		}

		return nil
	}
}

// fakeCluster is a cluster client with replica routing: its own Pipeline may land on any node (here it is counted
// and answers from the store), and MasterForKey gives the client of the primary.
type fakeCluster struct {
	store        *fakeStore
	primary      *redis.Client
	err          error // the answer of MasterForKey when it fails
	keys         []string
	routed       atomic.Int64 // pipelines that were sent through the routing client
	primaryCalls atomic.Int64
}

func (c *fakeCluster) Pipeline() redis.Pipeliner { //nolint:ireturn // the Client interface returns it
	c.routed.Add(1)

	return c.store.Pipeline()
}

func (c *fakeCluster) MasterForKey(_ context.Context, key string) (*redis.Client, error) {
	c.primaryCalls.Add(1)
	c.keys = append(c.keys, key)

	if c.err != nil {
		return nil, c.err
	}

	return c.primary, nil
}

func newFakeCluster(store *fakeStore) (*fakeCluster, *atomic.Int64) {
	hits := &atomic.Int64{}
	node := redis.NewClient(&redis.Options{Addr: nodeAddr})
	node.AddHook(primaryHook{store: store, pipelines: hits})

	return &fakeCluster{store: store, primary: node}, hits
}

func TestEverySnapshotReadGoesToThePrimaryOfItsKey(t *testing.T) {
	t.Parallel()

	const pageSize = 3

	store, segs := snapStore(pageSize)
	require.NoError(t, seeder(store)(snapAlpha, []string{"a", "b", "c", "d", "e", "f", "g"}))

	cluster, hits := newFakeCluster(store)

	prov, err := redissegment.New(cluster, &redissegment.Options{Segments: segs})
	require.NoError(t, err)

	token, err := prov.Version(context.Background(), snapAlpha)
	require.NoError(t, err)
	assert.Equal(t, "v:v1", token)

	var got []string

	for id, err := range prov.Members(context.Background(), snapAlpha) {
		require.NoError(t, err)

		got = append(got, id)
	}

	assert.ElementsMatch(t, []string{"a", "b", "c", "d", "e", "f", "g"}, got)
	assert.Equal(t, int64(4), hits.Load(), "one GET and three pages, all on the primary")
	assert.Zero(t, cluster.routed.Load(), "nothing went through the routing client")
	assert.Equal(
		t,
		[]string{snapVerKey, snapSetKey},
		cluster.keys,
		"the primary of the version key, then of the set: once each",
	)
}

func TestAMalformedPageIsAnErrorNeverTheEndOfTheScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reply   any
		name    string
		wantMsg string
	}{
		{name: "an empty cursor", reply: []any{"", []any{"a"}}, wantMsg: notNumber},
		{
			name:    "a cursor that is not a number",
			reply:   []any{"x1", []any{}},
			wantMsg: notNumber,
		},
		{name: "a negative cursor", reply: []any{"-1", []any{}}, wantMsg: notNumber},
		{
			name:    "a cursor that is not a string",
			reply:   []any{int64(0), []any{}},
			wantMsg: notList,
		},
		{name: "members that are not a list", reply: []any{"0", "a"}, wantMsg: notList},
		{
			name:    "a member that is not a string",
			reply:   []any{"0", []any{"a", int64(7)}},
			wantMsg: notList,
		},
		{name: "a reply of the wrong length", reply: []any{"0"}, wantMsg: notList},
		{
			name:    "a cursor that comes back",
			reply:   []any{"5", []any{"a"}},
			wantMsg: "returned the cursor that it was sent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, segs := snapStore(0)
			store.reply = func([]any) *fakeReply {
				return &fakeReply{value: tt.reply}
			}

			prov := snapProvider(t, store, segs, "")

			var seen []string

			var streamErr error

			for id, err := range prov.Members(context.Background(), snapAlpha) {
				if err != nil {
					streamErr = err

					break
				}

				seen = append(seen, id)
			}

			require.ErrorIs(t, streamErr, redissegment.ErrProtocol)
			require.ErrorContains(t, streamErr, tt.wantMsg)
			assert.Equal(t, "protocol", failureOf(prov), "the health of the provider says so")
		})
	}
}

func failureOf(prov *redissegment.Provider) string { return prov.Health().Failure }

func TestAMalformedPageKeepsTheLastKnownGood(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	seed := seeder(store)
	require.NoError(t, seed(snapAlpha, []string{"a", "b"}))

	prov := snapProvider(t, store, segs, "")
	holder, err := snapshot.New(snapAlpha, prov, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(context.Background()))

	// the writer moves the version, and the next scan answers a cursor that is not a number
	require.NoError(t, seed(snapAlpha, []string{"c"}))

	store.reply = func(args []any) *fakeReply {
		if name, _ := args[0].(string); name == "SSCAN" {
			return &fakeReply{value: []any{"", []any{}}}
		}

		return nil
	}

	require.ErrorIs(t, holder.Refresh(context.Background()), redissegment.ErrProtocol)
	assert.Equal(t, snapshot.ClassSet, holder.Status().LastError)
	assert.Equal(t, segment.Member, holder.Lookup("a"), "the old set survives")
	assert.Equal(t, segment.NotMember, holder.Lookup("c"))
}

func TestAStoredAbsentIsNotTheAbsentToken(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	prov := snapProvider(t, store, segs, "")

	missing, err := prov.Version(context.Background(), snapAlpha)
	require.NoError(t, err)
	assert.Equal(t, segment.AbsentVersion, missing)

	store.values[snapVerKey] = "absent"

	stored, err := prov.Version(context.Background(), snapAlpha)
	require.NoError(t, err)
	assert.NotEqual(t, segment.AbsentVersion, stored, "the writer stored the very word")

	// the holder follows: the set exists under a missing key, then the writer stores "absent" and replaces the set
	store.sets[snapSetKey] = map[string]bool{"a": true}
	delete(store.values, snapVerKey)

	holder, err := snapshot.New(snapAlpha, prov, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(context.Background()))
	assert.Equal(t, segment.Member, holder.Lookup("a"))

	store.sets[snapSetKey] = map[string]bool{"b": true}
	store.values[snapVerKey] = "absent"

	require.NoError(t, holder.Refresh(context.Background()))
	assert.Equal(t, segment.Member, holder.Lookup("b"), "the version moved: the set is pulled again")
}

func TestSnapshotCallsFeedTheHealthOfTheProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reply      *fakeReply
		name       string
		wantFail   string
		wantReady  bool
		cancelCall bool
	}{
		{name: "a call that works", wantReady: true},
		{name: "a version key that does not exist", wantReady: true},
		{name: "a server error", reply: &fakeReply{err: serverError{msg: "ERR boom"}}, wantFail: "server"},
		{name: "an ended context", cancelCall: true, wantFail: "canceled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, segs := snapStore(0)
			if tt.reply != nil {
				store.reply = func([]any) *fakeReply { return tt.reply }
			}

			prov := snapProvider(t, store, segs, "")
			ctx := context.Background()

			if tt.cancelCall {
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			_, _ = prov.Version(ctx, snapAlpha)

			health := prov.Health()
			assert.Equal(t, tt.wantFail, health.Failure)
			assert.Equal(t, tt.wantReady, health.Ready)
		})
	}
}

func TestInitProbesTheCommandsOfTheSnapshotMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		unknown      string
		name         string
		versionKey   string
		wantErrText  string
		wantCommands []string
		snapshot     bool
	}{
		{name: "lookup only: nothing extra", unknown: cmdSScan},
		{
			name: "snapshot with a version key", snapshot: true, versionKey: snapVerKey,
			wantCommands: []string{"sscan " + snapSetKey + " 0 COUNT 1", "get " + snapVerKey},
		},
		{
			name:         "snapshot without a version key",
			snapshot:     true,
			wantCommands: []string{"sscan " + snapSetKey + " 0 COUNT 1"},
		},
		{
			name: "SSCAN refused", snapshot: true, versionKey: snapVerKey, unknown: cmdSScan,
			wantErrText: "the command SSCAN is not available",
		},
		{
			name: "GET refused", snapshot: true, versionKey: snapVerKey, unknown: "get",
			wantErrText: "the command GET is not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				sets: map[string]map[string]bool{}, keys: map[string]bool{}, values: map[string]string{},
				unknown: map[string]bool{},
			}
			if tt.unknown != "" {
				store.unknown[tt.unknown] = true
			}

			segs := map[string]redissegment.SegmentOptions{
				snapAlpha: {Key: snapSetKey, VersionKey: tt.versionKey, Snapshot: tt.snapshot},
			}

			prov, err := redissegment.New(store, &redissegment.Options{Segments: segs})
			require.NoError(t, err)

			err = prov.Init(context.Background())

			if tt.wantErrText != "" {
				require.ErrorIs(t, err, redissegment.ErrInit)
				require.ErrorIs(t, err, redissegment.ErrServer)
				require.ErrorContains(t, err, tt.wantErrText)

				return
			}

			require.NoError(t, err)

			var probes []string

			for _, pipeline := range store.commands() {
				for _, cmd := range pipeline {
					if strings.HasPrefix(cmd, cmdSScan+" ") || strings.HasPrefix(cmd, "get ") {
						probes = append(probes, cmd)
					}
				}
			}

			assert.Equal(t, tt.wantCommands, probes, "the snapshot probes, each on the node of its key")
		})
	}
}

func TestAPrimaryThatCannotBeFoundFailsTheReadAndTheHealth(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	cluster, hits := newFakeCluster(store)
	cluster.err = serverError{msg: "ERR no node"}

	prov, err := redissegment.New(cluster, &redissegment.Options{Segments: segs})
	require.NoError(t, err)

	_, err = prov.Version(context.Background(), snapAlpha)
	require.ErrorContains(t, err, "finding the primary of a key")
	require.ErrorContains(t, firstError(prov.Members(context.Background(), snapAlpha)), "finding the primary of a key")
	assert.Zero(t, hits.Load(), "nothing was sent")
	assert.Equal(t, "server", prov.Health().Failure)
}

func TestSnapshotProbesAreDeduplicatedAndCountedInTheCap(t *testing.T) {
	t.Parallel()

	shared := redissegment.SegmentOptions{Key: snapSetKey, VersionKey: snapVerKey, Snapshot: true}

	distinct := func(n int) map[string]redissegment.SegmentOptions {
		segs := map[string]redissegment.SegmentOptions{}

		for i := range n {
			name := "s" + strconv.Itoa(100+i)
			segs[name] = redissegment.SegmentOptions{
				Key: name + ":{x}", VersionKey: "v:" + name, Snapshot: true, // each in its own namespace
			}
		}

		return segs
	}

	tests := []struct {
		segs        map[string]redissegment.SegmentOptions
		name        string
		wantScans   int
		wantGets    int
		wantLookups int
	}{
		{
			name: "three segments that share a set and a version key", wantScans: 1, wantGets: 1, wantLookups: 1,
			segs: map[string]redissegment.SegmentOptions{"a": shared, "b": shared, "c": shared},
		},
		{name: "two distinct segments", segs: distinct(2), wantScans: 2, wantGets: 2, wantLookups: 2},
		{
			// the snapshot probes come first: 10 segments make 20 probes, the cap of 16 keeps one for the lookups
			name: "the snapshot probes take the cap before the lookup probes", segs: distinct(10),
			wantScans: 8, wantGets: 7, wantLookups: 1,
		},
		{name: "the cap holds for 20 segments", segs: distinct(20), wantScans: 8, wantGets: 7, wantLookups: 1},
		{
			// 16 lookup namespaces and one snapshot segment: its two probes run, the lookups give way to 14
			name: "16 lookup namespaces and a snapshot segment", segs: lookupAndSnapshot(), wantScans: 1, wantGets: 1,
			wantLookups: 14,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := &fakeStore{
				sets:   map[string]map[string]bool{},
				keys:   map[string]bool{},
				values: map[string]string{},
			}

			prov, err := redissegment.New(store, &redissegment.Options{Segments: tt.segs})
			require.NoError(t, err)
			require.NoError(t, prov.Init(context.Background()))

			scans, gets, lookups := 0, 0, 0

			for _, pipeline := range store.commands() {
				for _, cmd := range pipeline {
					switch {
					case strings.HasPrefix(cmd, cmdSScan+" "):
						scans++
					case strings.HasPrefix(cmd, "get "):
						gets++
					case strings.HasPrefix(cmd, "smismember "):
						lookups++
					}
				}
			}

			assert.Equal(t, tt.wantScans, scans, "SSCAN probes")
			assert.Equal(t, tt.wantGets, gets, "GET probes")
			assert.Equal(t, tt.wantLookups, lookups, "lookup probes")
		})
	}
}

func TestSnapshotReadsAfterShutdownNeverReachTheStore(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	cluster, hits := newFakeCluster(store)

	prov, err := redissegment.New(cluster, &redissegment.Options{Segments: segs})
	require.NoError(t, err)
	require.NoError(t, prov.Shutdown(context.Background()))

	_, err = prov.Version(context.Background(), snapAlpha)
	require.ErrorIs(t, err, redissegment.ErrClosed)
	require.ErrorIs(t, firstError(prov.Members(context.Background(), snapAlpha)), redissegment.ErrClosed)
	assert.Zero(t, cluster.primaryCalls.Load(), "not even the search for the primary")
	assert.Zero(t, hits.Load())
}

func TestShutdownWaitsForASnapshotReadInFlight(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	entered, release := make(chan struct{}), make(chan struct{})

	store.exec = func(context.Context) error {
		close(entered)
		<-release

		return nil
	}

	var releaseOnce sync.Once

	open := func() { releaseOnce.Do(func() { close(release) }) }

	t.Cleanup(open) // a failed test must not leave the read at the gate

	prov := snapProvider(t, store, segs, "")

	var wait sync.WaitGroup

	wait.Go(func() {
		_, err := prov.Version(context.Background(), snapAlpha)
		assert.NoError(t, err, "the store ignores its context: the read ends with its answer once it is released")
	})

	<-entered

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(
		t,
		prov.Shutdown(ended),
		context.Canceled,
		"the read in flight is waited for, bounded by the context",
	)

	open()
	wait.Wait()
	require.NoError(t, prov.Shutdown(context.Background()))
}

// lookupAndSnapshot is 16 segments of the lookup mode, each in its own namespace, and one segment of the snapshot mode.
func lookupAndSnapshot() map[string]redissegment.SegmentOptions {
	segs := map[string]redissegment.SegmentOptions{
		"zsnap": {Key: "zsnap:{x}", VersionKey: "v:zsnap", Snapshot: true},
	}

	for i := range 16 {
		name := "l" + strconv.Itoa(100+i)
		segs[name] = redissegment.SegmentOptions{Key: name + ":{x}"}
	}

	return segs
}

func TestStartupProbesOfASnapshotGoToThePrimary(t *testing.T) {
	t.Parallel()

	store, _ := snapStore(0)
	cluster, hits := newFakeCluster(store)

	prov, err := redissegment.New(cluster, &redissegment.Options{Segments: map[string]redissegment.SegmentOptions{
		snapAlpha: {Key: snapSetKey, VersionKey: snapVerKey, Snapshot: true},
	}})
	require.NoError(t, err)
	require.NoError(t, prov.Init(context.Background()))

	assert.Equal(t, int64(2), hits.Load(), "the SSCAN and the GET of the snapshot probes ran on the primary")
	assert.Equal(t, []string{snapSetKey, snapVerKey}, cluster.keys, "the primary of the key of each probe")
	assert.Equal(t, int64(1), cluster.routed.Load(), "only the lookup probes went through the routing client")
}

func TestSnapshotsThatFillTheCapLeaveTheScriptDetection(t *testing.T) {
	t.Parallel()

	segs := map[string]redissegment.SegmentOptions{}

	for i := range 10 {
		name := "s" + strconv.Itoa(100+i)
		segs[name] = redissegment.SegmentOptions{Key: name + ":{x}", VersionKey: "v:" + name, Snapshot: true}
	}

	store := &fakeStore{sets: map[string]map[string]bool{}, keys: map[string]bool{}, values: map[string]string{}}

	prov, err := redissegment.New(store, &redissegment.Options{
		Segments: segs, Atomic: true, MissingKey: redissegment.MissingUnknown,
	})
	require.NoError(t, err)
	require.NoError(t, prov.Init(context.Background()))

	scripts, scans := 0, 0

	for _, pipeline := range store.commands() {
		for _, cmd := range pipeline {
			switch {
			case strings.HasPrefix(cmd, "eval_ro "):
				scripts++
			case strings.HasPrefix(cmd, cmdSScan+" "):
				scans++
			}
		}
	}

	assert.Equal(t, 1, scripts, "the read-only script is probed once, whatever the snapshot probes take")
	assert.Equal(t, 8, scans)
}
