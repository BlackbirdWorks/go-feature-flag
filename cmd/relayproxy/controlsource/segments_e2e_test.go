//go:build docker

package controlsource_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/testhelpers/gopherstack"
)

const (
	setOne       = "one"
	setTwo       = "two"
	keyOne       = "key-one"
	keyTwo       = "key-two"
	objPeople    = "people"
	objBroken    = "broken-people"
	peopleDoc    = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice]\n"
	peopleDocTwo = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice, bob]\n"
	brokenDoc    = "apiVersion: semaphore/v1\nkind: Segments\nsegments:\n  - name: beta\n    members: [alice, alice]\n"
)

// Published segments are accepted by the relay: a flag set that lists them is served, the object is in the status
// that /info shows, a bad version keeps the last good content, and an object that was never good degrades only the
// flag sets that list it. The evaluation of segments follows in a later change.
func TestE2E_PublishedSegmentsAreAcceptedByTheRelay(t *testing.T) {
	t.Parallel()

	srv := gopherstack.Connect(t)
	pub := &publication{
		t: t, srv: srv, bucket: srv.CreateBucket(t, "cs-segments"), wire: &wireLog{},
		flags:    map[string][]byte{objA: flagValueYAML(1), objB: flagValueYAML(2)},
		segments: map[string][]byte{objPeople: []byte(peopleDoc)},
		sets: []controlplane.FlagSet{
			{Name: setOne, Sources: []string{objA}, SegmentSources: []string{objPeople}, APIKeys: []string{keyOne}},
			{Name: setTwo, Sources: []string{objB}, APIKeys: []string{keyTwo}},
		},
	}
	pub.publish()

	rig := startE2E(t, pub)
	rig.settle(t)

	res := rig.nextRound(t)
	require.Equal(t, flagruntime.OutcomeApplied, res.Outcome, res.Detail)

	got, route := rig.value(keyOne)
	assert.Equal(t, flagruntime.RouteFlagSet, route, "the flag set that lists the segments object is served")
	assert.EqualValues(t, 1, got)

	status := rig.holder.Load().Status.Objects[objPeople]
	require.NotNil(t, status, "the object is in the status that /info shows")
	assert.Equal(t, flagruntime.KindSegments, status.Kind)
	assert.Equal(t, flagruntime.ObjectOK, status.State)
	assert.Equal(t, 1, status.Flags, "one segment")

	// a bad version of the object keeps the last good content, and both flag sets keep being served
	good := controlplane.ChecksumOf([]byte(peopleDoc)).Value
	pub.segments[objPeople] = []byte(brokenDoc)
	pub.publish()
	rig.poll(t)
	rig.nextRound(t)

	status = rig.holder.Load().Status.Objects[objPeople]
	assert.Equal(t, flagruntime.ObjectInvalid, status.State)
	assert.Equal(t, good, status.Checksum, "the last good content is kept")

	got, _ = rig.value(keyOne)
	assert.EqualValues(t, 1, got)

	got, _ = rig.value(keyTwo)
	assert.EqualValues(t, 2, got)

	// a new object that is bad from the start degrades only the flag set that lists it
	pub.segments[objPeople] = []byte(peopleDocTwo)
	pub.segments[objBroken] = []byte(brokenDoc)
	pub.sets = append(pub.sets, controlplane.FlagSet{
		Name: "three", Sources: []string{objB}, SegmentSources: []string{objBroken}, APIKeys: []string{"key-three"},
	})
	pub.publish()
	rig.poll(t)
	rig.nextRound(t)

	run := rig.holder.Load()
	assert.Equal(t, flagruntime.ObjectOK, run.Status.Objects[objPeople].State)
	assert.Equal(t, flagruntime.ObjectInvalid, run.Status.Objects[objBroken].State)

	got, _ = rig.value(keyOne)
	assert.EqualValues(t, 1, got, "the other flag sets are not affected")

	got, _ = rig.value(keyTwo)
	assert.EqualValues(t, 2, got)

	assert.Empty(
		t,
		run.FlagSets["three"].Flags,
		"the flag set that lists the broken object keeps the flags it had: none",
	)
}
