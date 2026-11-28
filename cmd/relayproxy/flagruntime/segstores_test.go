package flagruntime_test

import (
	"context"
	"iter"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// lookupStore serves the lookup mode only, snapshotStore the snapshot mode only, bothStore both: the capabilities of a
// store are the interfaces that its type implements.
type (
	lookupStore   struct{}
	snapshotStore struct{}
	bothStore     struct {
		lookupStore
		snapshotStore
	}
)

func (lookupStore) Contains(context.Context, []segment.Lookup) error { return nil }

func (snapshotStore) Version(context.Context, string) (string, error) { return "", nil }

func (snapshotStore) Members(context.Context, string) iter.Seq2[string, error] {
	return func(func(string, error) bool) {}
}

const (
	rejectPrefix  = "runtime: the dynamic segments do not fit the connections of the bootstrap file: "
	connAudiences = "audiences"
	tablePresent  = "present"
	tableAbsent   = "absent"
	kindRedis     = "redis"
)

func audienceDoc(connection, mode string) string {
	def := "  - name: audience\n    connection: " + connection + "\n"
	if mode != "" {
		def += "    mode: " + mode + "\n"
	}

	return segHeader + "  - name: beta\n    members: [alice]\n" + def
}

// A dynamic definition is checked against the connections of the bootstrap file when its segments object is read: the
// connection is there, its kind serves segments, and it serves the mode of the definition. A document that fails keeps
// the last good content of the object; the reasons name the field and never the content of the document.
func TestBuilderChecksDynamicDefinitions(t *testing.T) {
	t.Parallel()

	redis := func(store any) flagruntime.StoreTable {
		return flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: store}}
	}

	tests := []struct {
		stores     flagruntime.SegmentStores
		name       string
		doc        string
		wantReason string
		wantState  flagruntime.ObjectState
	}{
		{
			name: "snapshot default is served", stores: redis(bothStore{}), doc: audienceDoc(connAudiences, ""),
			wantState: flagruntime.ObjectOK,
		},
		{
			name: "lookup is served", stores: redis(bothStore{}), doc: audienceDoc(connAudiences, "lookup"),
			wantState: flagruntime.ObjectOK,
		},
		{
			name: "no stores is not checked", stores: nil, doc: audienceDoc("nowhere", ""),
			wantState: flagruntime.ObjectOK,
		},
		{
			name: "unknown connection", stores: redis(bothStore{}), doc: audienceDoc("nowhere", ""),
			wantState:  flagruntime.ObjectInvalid,
			wantReason: rejectPrefix + `segments[1].connection: "nowhere" is not a connection of the bootstrap file`,
		},
		{
			name:      "kind that serves no segment",
			stores:    flagruntime.StoreTable{connAudiences: {Kind: "s3"}},
			doc:       audienceDoc(connAudiences, ""),
			wantState: flagruntime.ObjectInvalid,
			wantReason: rejectPrefix +
				`segments[1].connection: "audiences" is a "s3" connection, which cannot serve segments`,
		},
		{
			name: "lookup that the store cannot serve", stores: redis(snapshotStore{}),
			doc: audienceDoc(connAudiences, "lookup"), wantState: flagruntime.ObjectInvalid,
			wantReason: rejectPrefix + `segments[1].mode: the store does not support the segment mode: ` +
				`segment "audience": mode "lookup", store "redis"`,
		},
		{
			name: "snapshot that the store cannot serve", stores: redis(lookupStore{}),
			doc: audienceDoc(connAudiences, ""), wantState: flagruntime.ObjectInvalid,
			wantReason: rejectPrefix + `segments[1].mode: the store does not support the segment mode: ` +
				`segment "audience": mode "snapshot", store "redis"`,
		},
		{
			name: "keys layout with snapshot", stores: redis(bothStore{}),
			doc:       segHeader + "  - name: audience\n    connection: audiences\n    layout: keys\n",
			wantState: flagruntime.ObjectInvalid,
			wantReason: "controlplane: invalid document: segments[0].layout: segment \"audience\": " +
				"layout \"keys\" with mode \"snapshot\": the keys layout is lookup-only",
		},
		{
			name: "version interval without a key", stores: redis(bothStore{}),
			doc: segHeader + "  - name: audience\n    connection: audiences\n" +
				"    version: {interval: 5s}\n",
			wantState:  flagruntime.ObjectInvalid,
			wantReason: "controlplane: invalid document: segments[0].version.interval: needs a key",
		},
		{
			name: "every bad definition has its reason", stores: redis(bothStore{}),
			doc: segHeader + "  - name: one\n    connection: nowhere\n  - name: two\n    connection: audiences\n" +
				"  - name: three\n    connection: elsewhere\n",
			wantState: flagruntime.ObjectInvalid,
			wantReason: rejectPrefix + `segments[0].connection: "nowhere" is not a connection of the bootstrap file; ` +
				`segments[2].connection: "elsewhere" is not a connection of the bootstrap file`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{SegmentStores: tt.stores})
			built := buildAndCommit(t, builder, content(1, objs{
				"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
				"f2":      flagsObj(1, "z"),
				segObject: segObj(tt.doc),
				setsName:  withSegmentSources(t),
			}))

			status := built.Runtime.Status.Objects[segObject]
			require.NotNil(t, status)
			assert.Equal(t, tt.wantState, status.State)
			assert.Equal(t, tt.wantReason, status.Reason)

			if tt.wantState == flagruntime.ObjectOK {
				assert.Empty(t, built.SegmentRejections)

				return
			}

			require.Len(t, built.SegmentRejections, 1)
			assert.Equal(t, flagruntime.SegmentRejection{
				Object: segObject, Checksum: built.SegmentRejections[0].Checksum, Reason: tt.wantReason,
			}, built.SegmentRejections[0])
			assert.NotEmpty(t, built.SegmentRejections[0].Checksum)
		})
	}
}

// A rejected document keeps the last good content: the flag set is served with the segments it had, the rejection is
// counted for the flag set and reported once, the same content again is not reported again, and a document that is
// fixed clears the count.
func TestARejectedDynamicDefinitionKeepsTheLastGoodContentAndIsReportedOnce(t *testing.T) {
	t.Parallel()

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{SegmentStores: stores})
	all := objs{
		"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(audienceDoc(connAudiences, "")),
		setsName:  withSegmentSources(t),
	}

	steps := []struct {
		name           string
		doc            string
		wantState      flagruntime.ObjectState
		wantCounts     flagruntime.SegmentCounts
		wantRejections int
	}{
		{
			name: "good", doc: audienceDoc(connAudiences, ""), wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1},
		},
		{
			name: "rejected", doc: audienceDoc("nowhere", ""), wantState: flagruntime.ObjectInvalid,
			wantCounts:     flagruntime.SegmentCounts{Static: 1, Dynamic: 1, RejectedDefinitions: 1},
			wantRejections: 1,
		},
		{
			name: "the same content again", doc: audienceDoc("nowhere", ""), wantState: flagruntime.ObjectInvalid,
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1, RejectedDefinitions: 1},
		},
		{
			name: "other bad content", doc: audienceDoc("elsewhere", ""), wantState: flagruntime.ObjectInvalid,
			wantCounts:     flagruntime.SegmentCounts{Static: 1, Dynamic: 1, RejectedDefinitions: 1},
			wantRejections: 1,
		},
		{
			name: "two bad definitions", doc: segHeader + "  - name: one\n    connection: nowhere\n" +
				"  - name: two\n    connection: elsewhere\n",
			wantState:      flagruntime.ObjectInvalid,
			wantCounts:     flagruntime.SegmentCounts{Static: 1, Dynamic: 1, RejectedDefinitions: 2},
			wantRejections: 1,
		},
		{
			name: "fixed", doc: audienceDoc(connAudiences, "lookup"), wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1},
		},
	}

	for gen, step := range steps {
		built := buildAndCommit(t, builder, content(uint64(gen+1), all.with(objs{segObject: segObj(step.doc)})))

		assert.Equal(t, step.wantState, built.Runtime.Status.Objects[segObject].State, step.name)
		assert.Len(t, built.SegmentRejections, step.wantRejections, step.name)

		require.Contains(t, built.Runtime.FlagSets, "a", step.name)
		assert.Equal(t, step.wantCounts, *built.Runtime.Status.FlagSets["a"].Segments, step.name)
		assert.Equal(t, flagruntime.StateServing, built.Runtime.FlagSets["a"].State, step.name)
	}
}

// The engine logs a rejected segments object once, with the reason, when the generation that met it is built, and does
// not log it again for the generations that carry the same content.
func TestEngineLogsARejectedSegmentsObjectOnce(t *testing.T) {
	t.Parallel()

	handler := newRecordingHandler()
	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	harness := newRigLogger(t, slog.New(handler), flagruntime.Hooks{}, flagruntime.WithSegmentStores(stores))
	harness.start()

	all := objs{
		"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(audienceDoc(connAudiences, "")),
		setsName:  withSegmentSources(t),
	}

	harness.feed(content(1, all))

	bad := all.with(objs{segObject: segObj(audienceDoc("nowhere", ""))})
	harness.feed(content(2, bad))
	harness.feed(content(3, bad.with(objs{"f2": flagsObj(2, "z")})))

	rejected := 0

	for record := range strings.SplitSeq(handler.text(), "\n") {
		if strings.HasPrefix(record, "segments object rejected: its last good content stays") {
			rejected++

			assert.Contains(t, record, "object="+segObject)
			assert.Contains(t, record, "generation=2")
			assert.Contains(t, record, `segments[1].connection: "nowhere" is not a connection of the bootstrap file`)
		}
	}

	assert.Equal(t, 1, rejected, "one record for the content, not one for each generation")
}

// A StoreTable answers for the connections it holds.
func TestStoreTable(t *testing.T) {
	t.Parallel()

	table := flagruntime.StoreTable{tablePresent: {Kind: kindRedis, Store: bothStore{}}}

	tests := []struct {
		name      string
		conn      string
		wantKind  string
		wantFound bool
	}{
		{name: tablePresent, conn: tablePresent, wantKind: kindRedis, wantFound: true},
		{name: tableAbsent, conn: tableAbsent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			facts, found := table.SegmentStore(tt.conn)
			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.wantKind, facts.Kind)
		})
	}
}
