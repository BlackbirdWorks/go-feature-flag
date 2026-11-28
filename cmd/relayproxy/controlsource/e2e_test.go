//go:build docker

package controlsource_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/controlsource"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

const (
	e2eConn     = "control"
	e2eChannel  = "stable"
	e2ePrefix   = "tenant/"
	e2eManifest = e2ePrefix + "manifests/stable.yaml"
	e2eFlag     = "f"
)

var errStoreDown = errors.New("the store is down")

// wireLog counts the HTTP requests that reach the emulator, and can make the store unreachable.
type wireLog struct {
	seen []string
	down atomic.Bool
	mu   sync.Mutex
}

func (w *wireLog) RoundTrip(req *http.Request) (*http.Response, error) {
	if w.down.Load() {
		return nil, errStoreDown
	}

	entry := req.Method + " " + req.URL.Path
	if req.Header.Get("If-None-Match") != "" {
		entry += " (conditional)"
	}

	w.mu.Lock()
	w.seen = append(w.seen, entry)
	w.mu.Unlock()

	return http.DefaultTransport.RoundTrip(req)
}

func (w *wireLog) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()

	out := w.seen
	w.seen = nil

	return out
}

// publication is what a publisher writes: flags objects by name, the document of flag sets, and the manifest.
type publication struct {
	t      *testing.T
	srv    *gopherstack.Server
	bucket string
	wire   *wireLog
	// objects are the names and bytes listed by the manifest, with the key of each.
	flags map[string][]byte
	// segments are the segments objects by name
	segments map[string][]byte
	sets     []controlplane.FlagSet
	gen      uint64
}

// object is a manifest entry with its bytes.
type object struct {
	entry controlplane.ManifestObject
	data  []byte
}

func (p *publication) entry(name, kind string, data []byte) object {
	p.t.Helper()

	sum := controlplane.ChecksumOf(data)
	key, err := controlplane.ObjectKey(sum, controlplane.FormatYAML)
	require.NoError(p.t, err)

	return object{data: data, entry: controlplane.ManifestObject{
		Name: name, Kind: kind, Connection: e2eConn, Key: e2ePrefix + key, Format: controlplane.FormatYAML,
		Checksum: sum, Size: int64(len(data)),
	}}
}

// publish writes every object, then the manifest of the next generation.
func (p *publication) publish() {
	p.t.Helper()

	p.gen++

	doc, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: p.sets})
	require.NoError(p.t, err)

	objs := make([]object, 0, 1+len(p.flags))
	objs = append(objs, p.entry("sets", controlplane.KindFlagSets, doc))

	for name, data := range p.flags {
		objs = append(objs, p.entry(name, controlplane.KindFlags, data))
	}

	for name, data := range p.segments {
		objs = append(objs, p.entry(name, controlplane.KindSegments, data))
	}

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: e2eChannel,
		Generation: p.gen,
	}

	for idx := range objs {
		p.srv.PutObject(p.t, p.bucket, objs[idx].entry.Key, objs[idx].data)

		manifest.Objects = append(manifest.Objects, objs[idx].entry)
	}

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(p.t, err)

	p.srv.PutObject(p.t, p.bucket, e2eManifest, data)
}

// keyOfFlags is the bucket key of a flags object of the current publication.
func (p *publication) keyOfFlags(name string) string {
	p.t.Helper()

	return p.entry(name, controlplane.KindFlags, p.flags[name]).entry.Key
}

func flagValueYAML(value int) []byte {
	return flagsYAML(value, e2eFlag)
}

// e2e is the reader, the source and the engine on an isolated bucket.
type e2e struct {
	pub     *publication
	sched   *tickScheduler
	reader  *manifestreader.Reader
	source  *controlsource.Source
	engine  *flagruntime.Engine
	holder  *flagruntime.Holder
	eval    *flagruntime.Evaluator
	rounds  chan flagruntime.RoundResult
	stopAll context.CancelFunc
	wg      sync.WaitGroup
}

func startE2E(t *testing.T, pub *publication) *e2e {
	t.Helper()

	fac := connection.New(connection.WithRoundTripper(pub.wire))
	require.NoError(t, fac.Set(e2eConn, &connection.S3Spec{
		Endpoint: pub.srv.Endpoint, Region: gopherstack.Region, Bucket: pub.bucket, PathStyle: true,
		Credentials: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret(gopherstack.AccessKey),
			SecretAccessKey: connection.NewSecret(gopherstack.SecretKey),
		},
	}))

	sched := &tickScheduler{entered: make(chan struct{}, 16), release: make(chan struct{})}
	reader, err := manifestreader.New(
		manifestreader.NewS3Store(fac, e2eConn, pub.bucket),
		&manifestreader.Config{
			ManifestKey: e2eManifest, Connection: e2eConn, Channel: e2eChannel, DisableJitter: true,
			FetchConcurrency: 1, DisableFetchSpread: true,
		},
		manifestreader.WithScheduler(sched), manifestreader.WithJitter(func() float64 { return 0 }))
	require.NoError(t, err)

	svc := flagdef.NewService()
	t.Cleanup(svc.Close)

	rig := &e2e{
		pub:    pub,
		sched:  sched,
		reader: reader,
		holder: flagruntime.NewHolder(),
		rounds: make(chan flagruntime.RoundResult, 64),
	}
	rig.engine = flagruntime.NewEngine(nil, svc, rig.holder, flagruntime.WithHooks(flagruntime.Hooks{
		AfterRound: func(res flagruntime.RoundResult) { rig.rounds <- res },
	}))
	rig.source = controlsource.New(reader, rig.engine, rig.holder, nil)
	rig.eval = flagruntime.NewEvaluator(svc, nil)

	ctx, cancel := context.WithCancel(t.Context())
	rig.stopAll = cancel

	rig.wg.Go(func() { assert.NoError(t, reader.Run(ctx)) })
	rig.wg.Go(func() { rig.engine.Run(ctx) })
	rig.wg.Go(func() { rig.source.Run(ctx) })

	t.Cleanup(func() {
		cancel()
		rig.wg.Wait()
	})

	return rig
}

// settle waits until the reader ended a round and waits for its next tick.
func (e *e2e) settle(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case <-e.sched.entered:
	case <-timer.C:
		require.FailNow(t, "the reader did not end its round")
	}
}

// poll ticks the reader once and waits for its round to end.
func (e *e2e) poll(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case e.sched.release <- struct{}{}:
	case <-timer.C:
		require.FailNow(t, "the reader is not waiting for a tick")
	}

	e.settle(t)
}

// nextRound waits for the engine to end the round of the snapshot the poll produced.
func (e *e2e) nextRound(t *testing.T) flagruntime.RoundResult {
	t.Helper()

	timer := time.NewTimer(waitBound)
	defer timer.Stop()

	select {
	case res := <-e.rounds:
		return res
	case <-timer.C:
		require.FailNow(t, "the engine ended no round")
	}

	return flagruntime.RoundResult{}
}

// value evaluates the flag of the flag set that a key routes to; ok is false for a key that does not route.
func (e *e2e) value(key string) (any, flagruntime.Route) {
	set, route := e.holder.Load().Route(key)
	if set == nil {
		return nil, route
	}

	res, _ := e.eval.RawVariation(context.Background(), set, e2eFlag, ffcontext.NewEvaluationContext("user"), nil)

	return res.Value, route
}

// The path of a publisher to a client: objects and a manifest in a bucket, the reader polling it, the source feeding
// the engine, and the evaluator answering for a key. Every step costs the requests it must and no more.
func TestE2E_PublishedFlagSetsAreServedLive(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	pub := &publication{
		t: t, srv: srv, bucket: srv.CreateBucket(t, "cs"), wire: &wireLog{},
		flags: map[string][]byte{objA: flagValueYAML(1), objB: flagValueYAML(2)},
		sets: []controlplane.FlagSet{
			{Name: "one", Sources: []string{objA}, APIKeys: []string{"key-one"}},
			{Name: "two", Sources: []string{objB}, APIKeys: []string{"key-two"}},
		},
	}
	pub.publish()

	rig := startE2E(t, pub)
	rig.settle(t)

	// 1. a key evaluates correctly: the manifest, then each object once
	res := rig.nextRound(t)
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	assert.ElementsMatch(t, []string{
		"GET /" + pub.bucket + "/" + e2eManifest,
		"GET /" + pub.bucket + "/" + pub.keyOfFlags(objA),
		"GET /" + pub.bucket + "/" + pub.keyOfFlags(objB),
		"GET /" + pub.bucket + "/" + pub.entry("sets", controlplane.KindFlagSets, mustDoc(t, pub.sets)).entry.Key,
	}, pub.wire.take(), "a cold start: the manifest and each object once, no list, no head")

	got, route := rig.value("key-one")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 1, got)

	got, _ = rig.value("key-two")
	assert.EqualValues(t, 2, got)

	// 2. one object edit is live after one poll, with exactly one extra object GET
	setTwo := rig.holder.Load().FlagSets["two"]
	pub.flags[objA] = flagValueYAML(10)
	pub.publish()
	pub.wire.take()
	rig.poll(t)
	rig.nextRound(t)

	assert.Equal(t, []string{
		"GET /" + pub.bucket + "/" + e2eManifest + " (conditional)",
		"GET /" + pub.bucket + "/" + pub.keyOfFlags(objA),
	}, pub.wire.take())

	got, _ = rig.value("key-one")
	assert.EqualValues(t, 10, got)
	assert.Same(t, setTwo, rig.holder.Load().FlagSets["two"], "the flag set of the other object is the same pointer")

	// 3. a new flag set with a new key works after one poll; it lists an object that is already held
	pub.sets = append(
		pub.sets,
		controlplane.FlagSet{Name: "three", Sources: []string{objB}, APIKeys: []string{"key-three"}},
	)
	pub.publish()
	pub.wire.take()
	rig.poll(t)
	rig.nextRound(t)

	assert.Equal(t, []string{
		"GET /" + pub.bucket + "/" + e2eManifest + " (conditional)",
		"GET /" + pub.bucket + "/" + pub.entry("sets", controlplane.KindFlagSets, mustDoc(t, pub.sets)).entry.Key,
	}, pub.wire.take(), "the document is the only new object: the flags it lists cost nothing")

	got, route = rig.value("key-three")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 2, got)

	// 4. a removed flag set's key is unknown after one poll
	pub.sets = pub.sets[1:]
	pub.publish()
	rig.poll(t)
	rig.nextRound(t)
	pub.wire.take()

	_, route = rig.value("key-one")
	assert.Equal(t, flagruntime.RouteUnknown, route)

	got, _ = rig.value("key-two")
	assert.EqualValues(t, 2, got, "the other flag sets keep serving")

	// 5. a corrupted object degrades only its flag sets: flags-b is overwritten after the manifest listed it, and the
	// other flag set (on flags-c, new) updates
	pub.flags[objC] = flagValueYAML(3)
	pub.sets = append(
		pub.sets,
		controlplane.FlagSet{Name: "four", Sources: []string{objC}, APIKeys: []string{"key-four"}},
	)
	previous := rig.holder.Load()
	pub.flags[objB] = flagValueYAML(20)
	pub.publish()

	// the same size, other bytes: the download is read and its checksum rejected
	corrupted := bytes.Clone(pub.flags[objB])
	corrupted[0] = 'X'
	pub.srv.PutObject(t, pub.bucket, pub.keyOfFlags(objB), corrupted)
	rig.poll(t)
	res = rig.nextRound(t)
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	run := rig.holder.Load()
	got, _ = rig.value("key-two")
	assert.EqualValues(t, 2, got, "the corrupted object keeps its last-known-good content")
	assert.Equal(t, flagruntime.StateDegraded, run.FlagSets["two"].State)
	assert.Equal(t, flagruntime.ObjectStale, run.Status.Objects[objB].State)
	assert.Equal(t, manifestreader.ReasonChecksumMismatch, run.Status.Objects[objB].Reason)

	got, route = rig.value("key-four")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.EqualValues(t, 3, got, "the flag set of the healthy object is served")
	assert.Equal(t, flagruntime.StateServing, run.FlagSets["four"].State)
	assert.Equal(t, flagruntime.StateDegraded, run.FlagSets["three"].State, "every flag set that lists the object")
	assert.Len(t, run.FlagSets["three"].Flags, 1)
	assert.NotSame(t, previous.FlagSets["three"], run.FlagSets["three"])

	// 6. with the store gone the runtime keeps serving, and the status says why
	pub.wire.down.Store(true)
	rig.poll(t)

	got, _ = rig.value("key-four")
	assert.EqualValues(t, 3, got)
	assert.Equal(t, manifestreader.PollError, rig.source.Status().Reader.Poll.Result)
	assert.Equal(t, run, rig.holder.Load(), "no new runtime was stored")
}

func mustDoc(t *testing.T, sets []controlplane.FlagSet) []byte {
	t.Helper()

	data, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: sets})
	require.NoError(t, err)

	return data
}
