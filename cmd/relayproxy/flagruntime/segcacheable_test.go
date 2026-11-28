package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

const (
	dynFlagName         = "dyn-flag"
	staticFlagName      = "static-flag"
	mixedFlagName       = "mixed-flag"
	plainFlagName       = "plain-flag"
	metaDynName         = "meta-dyn-flag"
	metaStaticName      = "meta-static-flag"
	carolKey            = "carol"
	betaSegmentName     = "beta"
	audienceSegmentName = "audience"
	teamKey             = "team"
	teamValue           = "core"
	vipKey              = "vip"
	useFlagsYAML        = `
dyn-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: vip, query: 'targetingKey eq "vip"', variation: on}
    - {name: audience, query: 'targetingKey in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
mixed-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: vip, query: 'targetingKey eq "vip"', variation: on}
    - {name: both, query: 'targetingKey in segment("beta") or targetingKey in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
static-flag:
  variations: {on: true, off: false}
  targeting:
    - {name: vip, query: 'targetingKey eq "vip"', variation: on}
    - {name: members, query: 'targetingKey in segment("beta")', variation: on}
  defaultRule: {name: default, variation: off}
plain-flag:
  variations: {on: true, off: false}
  defaultRule: {name: default, variation: off}
meta-dyn-flag:
  variations: {on: true, off: false}
  metadata: {semaphore_cacheable: true, gofeatureflag_cacheable: true, team: core}
  targeting:
    - {name: vip, query: 'targetingKey eq "vip"', variation: on}
    - {name: audience, query: 'targetingKey in segment("audience")', variation: on}
  defaultRule: {name: default, variation: off}
meta-static-flag:
  variations: {on: true, off: false}
  metadata: {semaphore_cacheable: true, gofeatureflag_cacheable: true, team: core}
  targeting:
    - {name: vip, query: 'targetingKey eq "vip"', variation: on}
  defaultRule: {name: default, variation: off}
`
	openFlagYAML = "other-flag:\n  variations: {on: true, off: false}\n  defaultRule: {variation: off}\n"
	useSegments  = segHeader + "  - name: beta\n    members: [alice]\n  - name: audience\n    connection: audiences\n"
)

func useRuntime(t *testing.T) *flagruntime.Runtime {
	t.Helper()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})

	return buildAndCommit(t, builder, content(1, objs{
		"f1":      obj(flagruntime.KindFlags, []byte(useFlagsYAML)),
		segObject: segObj(useSegments),
		setsName: setsObj(t,
			controlplane.FlagSet{
				Name: "a", Sources: src("f1"), SegmentSources: []string{segObject}, APIKeys: []string{keyA},
			},
			controlplane.FlagSet{Name: "bare", Sources: src("f1"), APIKeys: []string{keyB}},
		),
	})).Runtime
}

// A flag whose rules use a dynamic segment is never cacheable, whether or not the evaluation reached the segment: its
// members live in a store and change without a new flag. A flag with static segments only, and one with none, answer as
// they did.
func TestEvaluatorOfAFlagThatUsesADynamicSegmentIsNotCacheable(t *testing.T) {
	t.Parallel()

	run := useRuntime(t)
	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		name          string
		set           string
		flag          string
		key           string
		wantCacheable bool
	}{
		{name: "dynamic, decided before the segment", flag: dynFlagName, key: vipKey},
		{name: "dynamic, segment looked up", flag: dynFlagName, key: carolKey},
		{name: "dynamic and static, decided before", flag: mixedFlagName, key: vipKey},
		{name: "static only, decided before", flag: staticFlagName, key: vipKey, wantCacheable: true},
		{name: "static only, segment looked up", flag: staticFlagName, key: carolKey},
		{name: "no segment", flag: plainFlagName, key: vipKey, wantCacheable: true},
		{
			name:          "name that the flag set cannot resolve",
			set:           "bare",
			flag:          dynFlagName,
			key:           vipKey,
			wantCacheable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			set := run.FlagSets["a"]
			if tt.set != "" {
				set = run.FlagSets[tt.set]
			}

			raw, err := eval.RawVariation(set, tt.flag, ffcontext.NewEvaluationContext(tt.key), false)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCacheable, raw.Cacheable)

			typed, err := eval.BoolVariationDetails(set, tt.flag, ffcontext.NewEvaluationContext(tt.key), false)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCacheable, typed.Cacheable)
		})
	}
}

// The public flag set takes the segments of the flag set of each flag, so a flag of a public flag set that uses a
// dynamic segment is not cacheable there either.
func TestEvaluatorOfAPublicFlagThatUsesADynamicSegmentIsNotCacheable(t *testing.T) {
	t.Parallel()

	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})
	built := buildAndCommit(t, builder, content(1, objs{
		"f1":    obj(flagruntime.KindFlags, []byte(useFlagsYAML)),
		"f2":    obj(flagruntime.KindFlags, []byte(openFlagYAML)),
		"seg-1": segObj(useSegments),
		setsName: setsObj(t,
			controlplane.FlagSet{Name: "open-1", Sources: src("f1"), SegmentSources: []string{"seg-1"}},
			controlplane.FlagSet{Name: "open-2", Sources: src("f2")},
		),
	}))

	public := built.Runtime.Public
	require.NotNil(t, public)

	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		name          string
		flag          string
		wantCacheable bool
	}{
		{name: "dynamic flag", flag: dynFlagName},
		{name: "static only", flag: staticFlagName, wantCacheable: true},
		{name: "other flag set", flag: "other-flag", wantCacheable: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw, err := eval.RawVariation(public, tt.flag, ffcontext.NewEvaluationContext(vipKey), false)
			require.NoError(t, err)
			assert.Equal(t, tt.wantCacheable, raw.Cacheable)
		})
	}
}

// SegmentUse names the segments that the rules of a flag use and says whether one is dynamic; a flag that uses none, a
// flag that is not there and a nil flag set have no names.
func TestFlagSetSegmentUse(t *testing.T) {
	t.Parallel()

	run := useRuntime(t)
	set := run.FlagSets["a"]

	tests := []struct {
		set         *flagruntime.FlagSet
		name        string
		flag        string
		wantNames   []string
		wantDynamic bool
	}{
		{name: "dynamic", set: set, flag: dynFlagName, wantNames: []string{audienceSegmentName}, wantDynamic: true},
		{
			name:        "both kinds",
			set:         set,
			flag:        mixedFlagName,
			wantNames:   []string{audienceSegmentName, betaSegmentName},
			wantDynamic: true,
		},
		{name: "static", set: set, flag: staticFlagName, wantNames: []string{betaSegmentName}},
		{name: "no segment", set: set, flag: plainFlagName},
		{name: "unknown flag", set: set, flag: "nope"},
		{
			name: "names that the flag set cannot resolve", set: run.FlagSets["bare"], flag: dynFlagName,
			wantNames: []string{audienceSegmentName}, wantDynamic: true,
		},
		{name: "nil flag set", flag: dynFlagName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			names, dynamic := tt.set.SegmentUse(tt.flag)
			assert.Equal(t, tt.wantNames, names)
			assert.Equal(t, tt.wantDynamic, dynamic)
		})
	}
}

// A flag that uses a dynamic segment never tells a client that its value can be held, even when its own metadata says
// so: the cacheable keys that the flag wrote are removed from the single and from the bulk answer, and the other keys
// stay. A flag with no dynamic segment keeps what it wrote.
func TestEvaluatorDropsTheCacheableKeysOfAFlagThatUsesADynamicSegment(t *testing.T) {
	t.Parallel()

	set := useRuntime(t).FlagSets["a"]
	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		wantMetadata map[string]any
		name         string
		flag         string
	}{
		{name: "dynamic metadata", flag: metaDynName, wantMetadata: map[string]any{teamKey: teamValue}},
		{
			name: "no dynamic segment",
			flag: metaStaticName,
			wantMetadata: map[string]any{
				teamKey:                   teamValue,
				"semaphore_cacheable":     true,
				"gofeatureflag_cacheable": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			evalCtx := ffcontext.NewEvaluationContext(vipKey)

			raw, err := eval.RawVariation(set, tt.flag, evalCtx, false)
			require.NoError(t, err)
			assert.Equal(t, tt.wantMetadata, withoutRule(raw.Metadata))

			bulk := eval.GetFlagStates(set, evalCtx, []string{tt.flag})
			assert.Equal(t, tt.wantMetadata, withoutRule(bulk.GetFlags()[tt.flag].Metadata))

			all := eval.AllFlagsState(set, evalCtx)
			assert.Equal(t, tt.wantMetadata, withoutRule(all.GetFlags()[tt.flag].Metadata))
		})
	}
}

// withoutRule is the metadata without the name of the rule that the evaluation adds.
func withoutRule(metadata map[string]any) map[string]any {
	out := map[string]any{}

	for key, value := range metadata {
		if key != "evaluatedRuleName" {
			out[key] = value
		}
	}

	return out
}

// A flag set that is only a placeholder (its entry is invalid and never was valid) still reports the segments of the
// segments objects that it names, and follows them when they change.
func TestPlaceholderReportsTheSegmentsOfItsSources(t *testing.T) {
	t.Parallel()

	stores := flagruntime.StoreTable{connAudiences: {Kind: kindRedis, Store: bothStore{}}}
	builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{SegmentStores: stores})
	invalid := setsObj(t, controlplane.FlagSet{Name: "a", SegmentSources: []string{segObject}, APIKeys: []string{keyA}})
	all := objs{"f1": flagsObj(1, "x"), setsName: invalid}

	steps := []struct {
		name       string
		doc        string
		more       string
		wantCounts flagruntime.SegmentCounts
	}{
		{name: "one definition is rejected", doc: audienceDoc("nowhere", ""),
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1, RejectedDefinitions: 1}},
		{name: "the document is fixed", doc: audienceDoc(connAudiences, ""),
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1}},
		{name: "rejected again, with a new object (a full pass)", doc: audienceDoc("elsewhere", ""), more: "f9",
			wantCounts: flagruntime.SegmentCounts{Static: 1, Dynamic: 1, RejectedDefinitions: 1}},
	}

	for gen, step := range steps {
		next := all.with(objs{segObject: segObj(step.doc)})
		if step.more != "" {
			next = next.with(objs{step.more: flagsObj(1, "y")})
		}

		built := buildAndCommit(t, builder, content(uint64(gen+1), next))

		status := built.Runtime.Status.FlagSets["a"]
		require.NotNil(t, status, step.name)
		assert.Equal(t, flagruntime.StatePending, status.State, step.name)
		require.NotNil(t, status.Segments, step.name)
		assert.Equal(t, step.wantCounts, *status.Segments, step.name)
	}
}
