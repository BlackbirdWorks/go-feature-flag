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
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
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
	rejectedNoticePrefix = "the dynamic definitions that the connections cannot serve are rejected, " +
		"the others are applied: "
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
// connection is there, its kind serves segments, and it serves the mode of the definition. A definition that fails is
// rejected alone: the object and its other definitions are applied, the segment stays in the catalog and answers as a
// segment without a provider, and the flag set is served. A document that is not valid is rejected whole. The reasons
// name the field and never the content of the document.
func TestBuilderChecksDynamicDefinitions(t *testing.T) {
	t.Parallel()

	redis := func(store any) flagruntime.StoreTable {
		return flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: store}}
	}

	tests := []struct {
		stores        flagruntime.SegmentStores
		name          string
		doc           string
		wantNotice    string
		wantObjReason string
		wantObjState  flagruntime.ObjectState
		wantRejected  int
		wantServed    bool
	}{
		{
			name: "snapshot default is served", stores: redis(bothStore{}), doc: audienceDoc(connAudiences, ""),
			wantObjState: flagruntime.ObjectOK, wantServed: true,
		},
		{
			name: "lookup is served", stores: redis(bothStore{}), doc: audienceDoc(connAudiences, "lookup"),
			wantObjState: flagruntime.ObjectOK, wantServed: true,
		},
		{
			name: "no stores is not checked", stores: nil, doc: audienceDoc("nowhere", ""),
			wantObjState: flagruntime.ObjectOK, wantServed: true,
		},
		{
			name: "unknown connection", stores: redis(bothStore{}), doc: audienceDoc("nowhere", ""),
			wantObjState: flagruntime.ObjectOK, wantServed: true, wantRejected: 1,
			wantNotice: `segments[1].connection: "nowhere" is not a connection of the bootstrap file`,
		},
		{
			name:         "kind that serves no segment",
			stores:       flagruntime.StoreTable{connAudiences: {Kind: "s3"}},
			doc:          audienceDoc(connAudiences, ""),
			wantObjState: flagruntime.ObjectOK, wantServed: true, wantRejected: 1,
			wantNotice: `segments[1].connection: "audiences" is a "s3" connection, which cannot serve segments`,
		},
		{
			name: "lookup that the store cannot serve", stores: redis(snapshotStore{}),
			doc: audienceDoc(connAudiences, "lookup"), wantObjState: flagruntime.ObjectOK, wantServed: true,
			wantRejected: 1,
			wantNotice: `segments[1].mode: the store does not support the segment mode: ` +
				`segment "audience": mode "lookup", store "redis"`,
		},
		{
			name: "snapshot that the store cannot serve", stores: redis(lookupStore{}),
			doc: audienceDoc(connAudiences, ""), wantObjState: flagruntime.ObjectOK, wantServed: true,
			wantRejected: 1,
			wantNotice: `segments[1].mode: the store does not support the segment mode: ` +
				`segment "audience": mode "snapshot", store "redis"`,
		},
		{
			name: "every bad definition has its reason", stores: redis(bothStore{}),
			doc: segHeader + "  - name: one\n    connection: nowhere\n  - name: two\n    connection: audiences\n" +
				"  - name: three\n    connection: elsewhere\n",
			wantObjState: flagruntime.ObjectOK, wantServed: true, wantRejected: 2,
			wantNotice: `segments[0].connection: "nowhere" is not a connection of the bootstrap file; ` +
				`segments[2].connection: "elsewhere" is not a connection of the bootstrap file`,
		},
		{
			name: "keys layout with snapshot", stores: redis(bothStore{}),
			doc:          segHeader + "  - name: audience\n    connection: audiences\n    layout: keys\n",
			wantObjState: flagruntime.ObjectInvalid,
			wantObjReason: "controlplane: invalid document: segments[0].layout: segment \"audience\": " +
				"layout \"keys\" with mode \"snapshot\": the keys layout is lookup-only",
			wantNotice: "controlplane: invalid document: segments[0].layout: segment \"audience\": " +
				"layout \"keys\" with mode \"snapshot\": the keys layout is lookup-only",
		},
		{
			name: "version interval without a key", stores: redis(bothStore{}),
			doc: segHeader + "  - name: audience\n    connection: audiences\n" +
				"    version: {interval: 5s}\n",
			wantObjState:  flagruntime.ObjectInvalid,
			wantObjReason: "controlplane: invalid document: segments[0].version.interval: needs a key",
			wantNotice:    "controlplane: invalid document: segments[0].version.interval: needs a key",
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
			assert.Equal(t, tt.wantObjState, status.State)
			assert.Equal(t, tt.wantObjReason, status.Reason)

			setA := built.Runtime.Status.FlagSets["a"]
			require.NotNil(t, setA)
			require.NotNil(t, setA.Segments)
			assert.Equal(t, tt.wantRejected, setA.Segments.RejectedDefinitions)
			assert.Equal(t, tt.wantServed, setA.State == flagruntime.StateServing, "the flag set is served")

			if tt.wantNotice == "" {
				assert.Empty(t, built.SegmentRejections)

				return
			}

			require.Len(t, built.SegmentRejections, 1)

			notice := built.SegmentRejections[0]
			assert.NotEmpty(t, notice.Checksum)
			assert.Equal(t, segObject, notice.Object)
			assert.Equal(t, tt.wantRejected, notice.Definitions)

			if tt.wantRejected > 0 {
				assert.Equal(t, rejectedNoticePrefix+tt.wantNotice, notice.Reason)
			} else {
				assert.Equal(t, tt.wantNotice, notice.Reason)
			}
		})
	}
}

// A rejected definition answers as a segment without a provider: not a member, NOT_CONFIGURED, never cacheable; a
// static segment of the same document keeps answering from its members.
func TestARejectedDynamicDefinitionAnswersNotConfigured(t *testing.T) {
	t.Parallel()

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{SegmentStores: stores})
	built := buildAndCommit(t, builder, content(1, objs{
		"f1":      obj(flagruntime.KindFlags, []byte(useFlagsYAML)),
		segObject: segObj(audienceDoc("nowhere", "")),
		setsName: setsObj(t, controlplane.FlagSet{
			Name: "a", Sources: src("f1"), SegmentSources: []string{segObject}, APIKeys: []string{keyA},
		}),
	}))

	set := built.Runtime.FlagSets["a"]
	require.NotNil(t, set, "the flag set is served on its first load")

	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		name            string
		flag            string
		key             string
		wantStatus      segment.Status
		wantValue       bool
		wantCacheable   bool
		wantRequiresSrv bool
	}{
		{
			name: "rejected definition", flag: dynFlagName, key: carolKey, wantStatus: segment.StatusNotConfigured,
			wantRequiresSrv: true,
		},
		{
			name:       "static segment of the same document",
			flag:       staticFlagName,
			key:        "alice",
			wantStatus: segment.StatusHealthy,
			wantValue:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := eval.BoolVariationDetails(
				t.Context(),
				set,
				tt.flag,
				ffcontext.NewEvaluationContext(tt.key),
				false,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, got.Value)
			assert.Equal(t, tt.wantStatus, got.SegmentStatus)
			assert.Equal(t, tt.wantCacheable, got.Cacheable)

			_, requires := set.SegmentUse(tt.flag)
			assert.Equal(t, tt.wantRequiresSrv, requires)
		})
	}
}

// A document that is not valid keeps the last good content of the object (whole), a rejected definition does not: the
// counts follow the last content, a rejection is reported once for its content, and a fixed document clears it.
func TestRejectionsAreReportedOnceAndCounted(t *testing.T) {
	t.Parallel()

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{SegmentStores: stores})
	all := objs{
		"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(audienceDoc(connAudiences, "")),
		setsName:  withSegmentSources(t),
	}

	// the flags of the fixture use "staff", which no document of this test defines
	staffOnly := []string{staffName}
	invalid := segHeader + "  - name: audience\n    connection: audiences\n    layout: keys\n"

	steps := []struct {
		name           string
		doc            string
		wantState      flagruntime.ObjectState
		wantCounts     flagruntime.SegmentCounts
		wantRejections int
	}{
		{
			name: "good", doc: audienceDoc(connAudiences, ""), wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1, Unresolved: 1, UnresolvedNames: staffOnly},
		},
		{
			name: "definition rejected", doc: audienceDoc("nowhere", ""), wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{
				Static: 1, Dynamic: 1, RejectedDefinitions: 1, Unresolved: 1, UnresolvedNames: staffOnly,
			},
			wantRejections: 1,
		},
		{
			name: "the same content again", doc: audienceDoc("nowhere", ""), wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{
				Static: 1, Dynamic: 1, RejectedDefinitions: 1, Unresolved: 1, UnresolvedNames: staffOnly,
			},
		},
		{
			name: "other bad content", doc: audienceDoc("elsewhere", ""), wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{
				Static: 1, Dynamic: 1, RejectedDefinitions: 1, Unresolved: 1, UnresolvedNames: staffOnly,
			},
			wantRejections: 1,
		},
		{
			name: "two bad definitions", doc: segHeader + "  - name: one\n    connection: nowhere\n" +
				"  - name: two\n    connection: elsewhere\n",
			wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{
				Dynamic:             2,
				RejectedDefinitions: 2,
				Unresolved:          2,
				UnresolvedNames:     []string{betaSegmentName, staffName},
			},
			wantRejections: 1,
		},
		{
			name: "invalid document keeps the last good one", doc: invalid, wantState: flagruntime.ObjectInvalid,
			wantCounts: flagruntime.SegmentCounts{
				Dynamic:             2,
				RejectedDefinitions: 2,
				Unresolved:          2,
				UnresolvedNames:     []string{betaSegmentName, staffName},
			},
			wantRejections: 1,
		},
		{
			name: "fixed", doc: audienceDoc(connAudiences, "lookup"), wantState: flagruntime.ObjectOK,
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1, Unresolved: 1, UnresolvedNames: staffOnly},
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

// The engine logs the definitions that the connections cannot serve once, with the reason, when the generation that
// met them is built, and does not log them again for the generations that carry the same content.
func TestEngineLogsRejectedDefinitionsOnce(t *testing.T) {
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
		if !strings.HasPrefix(record, "dynamic segment definitions rejected") {
			continue
		}

		rejected++

		assert.Contains(t, record, "object="+segObject)
		assert.Contains(t, record, "generation=2")
		assert.Contains(t, record, "definitions=1")
		assert.Contains(t, record, `segments[1].connection: "nowhere" is not a connection of the bootstrap file`)
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
