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
	dynFlagName     = "dyn-flag"
	staticFlagName  = "static-flag"
	mixedFlagName   = "mixed-flag"
	plainFlagName   = "plain-flag"
	carolKey        = "carol"
	betaSegmentName = "beta"
	vipKey          = "vip"
	useFlagsYAML    = `
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
		setsName: setsObj(t, controlplane.FlagSet{
			Name: "a", Sources: src("f1"), SegmentSources: []string{segObject}, APIKeys: []string{keyA},
		}),
	})).Runtime
}

// A flag whose rules use a dynamic segment is never cacheable, whether or not the evaluation reached the segment: its
// members live in a store and change without a new flag. A flag with static segments only, and one with none, answer as
// they did.
func TestEvaluatorOfAFlagThatUsesADynamicSegmentIsNotCacheable(t *testing.T) {
	t.Parallel()

	set := useRuntime(t).FlagSets["a"]
	eval := flagruntime.NewEvaluator(newService(t), nil)

	tests := []struct {
		name          string
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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
		{name: "dynamic", flag: dynFlagName},
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

	set := useRuntime(t).FlagSets["a"]

	tests := []struct {
		set         *flagruntime.FlagSet
		name        string
		flag        string
		wantNames   []string
		wantDynamic bool
	}{
		{name: "dynamic", set: set, flag: dynFlagName, wantNames: []string{"audience"}, wantDynamic: true},
		{
			name: "both kinds", set: set, flag: mixedFlagName, wantNames: []string{"audience", betaSegmentName},
			wantDynamic: true,
		},
		{name: "static", set: set, flag: staticFlagName, wantNames: []string{betaSegmentName}},
		{name: "no segment", set: set, flag: plainFlagName},
		{name: "unknown flag", set: set, flag: "nope"},
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
