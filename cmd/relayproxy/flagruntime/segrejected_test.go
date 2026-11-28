package flagruntime_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	connNowhere     = "nowhere"
	policyFlagsYAML = `
member-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: audience, query: 'targetingKey in segment("lenient")', variation: on}
  defaultRule: {name: default, variation: off}
match-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: audience, query: 'targetingKey in segment("strict")', variation: on, onError: match}
  defaultRule: {name: default, variation: off}
negated-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: outside, query: 'not (targetingKey in segment("keyed"))', variation: on}
  defaultRule: {name: default, variation: off}
`
	// lenient grants membership when a lookup cannot be answered, strict leaves it unknown (the rule decides).
	policySegments = segHeader +
		"  - name: lenient\n    connection: audiences\n    onError: member\n" +
		"  - name: strict\n    connection: audiences\n    onError: unknown\n" +
		"  - name: keyed\n    connection: audiences\n    onError: member\n    missingKey: unknown\n"
)

func policyRuntime(t *testing.T, connection string) *flagruntime.Runtime {
	t.Helper()

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{SegmentStores: stores})
	doc := strings.ReplaceAll(policySegments, "connection: audiences", "connection: "+connection)

	return buildAndCommit(t, builder, content(1, objs{
		"f1":      obj(flagruntime.KindFlags, []byte(policyFlagsYAML)),
		segObject: segObj(doc),
		setsName: setsObj(t, controlplane.FlagSet{
			Name: "a", Sources: src("f1"), SegmentSources: []string{segObject}, APIKeys: []string{keyA},
		}),
	})).Runtime
}

// A rejected definition answers not a member, whatever its failure policy says, and a rule that says onError: match
// does not turn it into a match: it is a configuration error of this relay, and it never grants membership. The same
// definitions served by their connection keep their policy.
func TestARejectedDefinitionNeverGrantsMembership(t *testing.T) {
	t.Parallel()

	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		name       string
		connection string
		flag       string
		wantValue  bool
	}{
		{name: "rejected, definition onError member", connection: connNowhere, flag: "member-flag"},
		{name: "rejected, rule onError match", connection: connNowhere, flag: "match-flag"},
		{name: "rejected, negated clause", connection: connNowhere, flag: "negated-flag", wantValue: true},
		{name: "served, negated clause", connection: connAudiences, flag: "negated-flag"},
		{name: "served, definition onError member", connection: connAudiences, flag: "member-flag", wantValue: true},
		{name: "served, rule onError match", connection: connAudiences, flag: "match-flag", wantValue: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set := policyRuntime(t, tt.connection).FlagSets["a"]

			got, err := eval.BoolVariationDetails(
				t.Context(),
				set,
				tt.flag,
				ffcontext.NewEvaluationContext("alice"),
				false,
			)
			require.NoError(t, err)
			assert.Equal(t, tt.wantValue, got.Value)
			assert.Equal(t, segment.StatusNotConfigured, got.SegmentStatus)
		})
	}
}

// Ten thousand rejected definitions are all counted and all answer not configured.
func TestManyRejectedDefinitionsAreAllCounted(t *testing.T) {
	t.Parallel()

	const total = 10_000

	var doc strings.Builder

	doc.WriteString(segHeader)

	for i := range total {
		doc.WriteString("  - name: seg-" + strconv.Itoa(i) + "\n    connection: nowhere\n")
	}

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{SegmentStores: stores})
	built := buildAndCommit(t, builder, content(1, objs{
		"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
		segObject: segObj(doc.String()),
		setsName: setsObj(t, controlplane.FlagSet{
			Name: "a", Sources: src("f1"), SegmentSources: []string{segObject}, APIKeys: []string{keyA},
		}),
	}))

	status := built.Runtime.Status.FlagSets["a"]
	require.NotNil(t, status)
	require.NotNil(t, status.Segments)
	assert.Equal(t, flagruntime.SegmentCounts{
		Dynamic:             total,
		RejectedDefinitions: total,
		Unresolved:          2,
		UnresolvedNames:     []string{betaSegmentName, staffName},
	}, *status.Segments)
}

func BenchmarkBuildRejectedDefinitions(b *testing.B) {
	for _, total := range []int{1_000, 10_000} {
		var doc strings.Builder

		doc.WriteString(segHeader)

		for i := range total {
			doc.WriteString("  - name: seg-" + strconv.Itoa(i) + "\n    connection: nowhere\n")
		}

		stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
		input := content(1, objs{
			"f1":      obj(flagruntime.KindFlags, []byte(betaFlagYAML)),
			segObject: segObj(doc.String()),
			setsName: setsObj(b, controlplane.FlagSet{
				Name: "a", Sources: src("f1"), SegmentSources: []string{segObject}, APIKeys: []string{keyA},
			}),
		})

		b.Run(strconv.Itoa(total), func(b *testing.B) {
			for b.Loop() {
				builder := flagruntime.NewBuilder(newService(b), flagruntime.BuilderOptions{SegmentStores: stores})

				_, err := builder.Build(b.Context(), input)
				require.NoError(b, err)
			}
		})
	}
}

// The rejection is applied over what the document says: a rejected definition keeps the failure policy and the missing
// key of a segment that was refused, whatever its own fields are, and a served one keeps its own.
func TestARejectedDefinitionOverridesItsDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		connection     string
		wantOnError    segment.OnError
		wantMissingKey segment.MissingKey
	}{
		{
			name: "rejected", connection: connNowhere, wantOnError: segment.OnErrorNotMember,
			wantMissingKey: segment.MissingNotMember,
		},
		{
			name:           "served",
			connection:     connAudiences,
			wantOnError:    segment.OnErrorMember,
			wantMissingKey: segment.MissingUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			def, ok := policyRuntime(t, tt.connection).FlagSets["a"].SegmentCatalog().Lookup("keyed")

			require.True(t, ok)
			assert.Equal(t, tt.wantOnError, def.OnError)
			assert.Equal(t, tt.wantMissingKey, def.MissingKey)
		})
	}
}
