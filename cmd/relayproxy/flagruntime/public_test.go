package flagruntime_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

const (
	pubOne = "pub-one"
	pubTwo = "pub-two"
)

// The flag sets published without a key form the public namespace: a request without a key is served by it, a key that
// is given never is, and what is public follows the generations.
func TestPublicFlagSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content    *flagruntime.Content
		name       string
		wantFlags  []string
		wantSets   []string
		wantRoute  flagruntime.Route
		wantPublic bool
	}{
		{
			name:      "no flag set without keys",
			wantRoute: flagruntime.RouteUnknown,
			content: content(
				1,
				objs{flagsName: flagsObj(1, "f"), setsName: setsObj(t, fset("keyed", src(flagsName), keyA))},
			),
		},
		{
			name: "one flag set without keys", wantRoute: flagruntime.RouteFlagSet, wantPublic: true,
			wantFlags: []string{"f"}, wantSets: []string{pubOne},
			content: content(1, objs{
				flagsName: flagsObj(
					1,
					"f",
				),
				setsName: setsObj(t, fset("keyed", src(flagsName), keyA), fset(pubOne, src(flagsName))),
			}),
		},
		{
			name: "two flag sets without keys are one namespace", wantRoute: flagruntime.RouteFlagSet, wantPublic: true,
			wantFlags: []string{"f", "g"}, wantSets: []string{pubOne, pubTwo},
			content: content(1, objs{
				"o1": flagsObj(1, "f"), "o2": flagsObj(2, "g"),
				setsName: setsObj(t, fset(pubOne, src("o1")), fset(pubTwo, src("o2"))),
			}),
		},
		{
			name: "a flag set without keys that is not loaded", wantRoute: flagruntime.RoutePending, wantPublic: true,
			wantSets: []string{pubOne},
			content:  content(1, objs{setsName: setsObj(t, fset(pubOne, src("not-there")))}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness := routeRig(t, slog.DiscardHandler, tt.content)
			run := harness.holder.Load()

			set, route := run.Route("")
			assert.Equal(t, tt.wantRoute, route)
			assert.Equal(t, tt.wantPublic, set != nil)

			if set != nil {
				assert.Equal(t, flagruntime.PublicName, set.Name)
				assert.Equal(t, tt.wantFlags, flagNames(set))
			}

			status := run.Status.Public
			require.NotNil(t, status)
			assert.Equal(t, tt.wantPublic, status.Present)
			assert.Equal(t, len(tt.wantFlags), status.Flags)

			wantSets := tt.wantSets
			if wantSets == nil {
				wantSets = []string{}
			}

			assert.Equal(t, wantSets, status.Sets)

			// a key that is given never routes to the public flags
			_, wrong := run.Route("not-a-key")
			assert.Equal(t, flagruntime.RouteUnknown, wrong)
		})
	}
}

// A flag key that two public flag sets define makes the generation rejected: the relay keeps the last known good, and
// the log says the key and the two flag sets, never a key of a flag set.
func TestPublicFlagKeyInTwoFlagSetsRejectsTheGeneration(t *testing.T) {
	t.Parallel()

	handler := newRecordingHandler()
	harness := routeRig(t, slog.New(handler).Handler(), content(1, objs{
		"o1": flagsObj(1, "f"), setsName: setsObj(t, fset(pubOne, src("o1")), fset("keyed", src("o1"), keyA)),
	}))

	res := harness.feed(content(2, objs{
		"o1": flagsObj(1, "f"), "o2": flagsObj(2, "f", "g"),
		setsName: setsObj(t, fset(pubOne, src("o1")), fset(pubTwo, src("o2")), fset("keyed", src("o1"), keyA)),
	}))

	assert.Equal(t, flagruntime.OutcomeRejected, res.Outcome)
	assert.Equal(t, flagruntime.CodeBuildFailed, res.Code)
	assert.Contains(t, res.Detail, `"f" is defined in pub-one and pub-two`)
	assert.NotContains(t, res.Detail, keyA)

	run := harness.holder.Load()
	assert.Equal(t, uint64(1), run.Generation, "the last known good is served")

	set, route := run.Route("")
	assert.Equal(t, flagruntime.RouteFlagSet, route)
	assert.Equal(t, []string{"f"}, flagNames(set))
}

// The public view is made once per generation, and shared when its sources did not change: a generation that changes
// another flag set keeps the same public flag set, one that changes a public source makes a new one, and the change
// of the public flags is reported under the name of the public flag set.
func TestPublicViewIsSharedAndReported(t *testing.T) {
	t.Parallel()

	b := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{})

	first := buildAndCommit(t, b, content(1, objs{
		"o1": flagsObj(
			1,
			"f",
		),
		"o2":     flagsObj(1, "k"),
		setsName: setsObj(t, fset(pubOne, src("o1")), fset("keyed", src("o2"), keyA)),
	}))
	assert.Contains(t, rebuiltNames(first), flagruntime.PublicName, "a new public flag set is reported")
	assert.Same(
		t,
		first.Runtime.FlagSets[pubOne].Flags["f"],
		first.Runtime.Public.Flags["f"],
		"one source: no copy of the flags",
	)

	// only the keyed flag set changes
	second := buildAndCommit(t, b, content(2, objs{
		"o1": flagsObj(
			1,
			"f",
		),
		"o2":     flagsObj(2, "k"),
		setsName: setsObj(t, fset(pubOne, src("o1")), fset("keyed", src("o2"), keyA)),
	}))
	assert.Same(t, first.Runtime.Public, second.Runtime.Public, "the public flag set is kept")
	assert.NotContains(t, rebuiltNames(second), flagruntime.PublicName)

	// a public source changes
	third := buildAndCommit(t, b, content(3, objs{
		"o1": flagsObj(
			5,
			"f",
		),
		"o2":     flagsObj(2, "k"),
		setsName: setsObj(t, fset(pubOne, src("o1")), fset("keyed", src("o2"), keyA)),
	}))
	assert.NotSame(t, second.Runtime.Public, third.Runtime.Public)
	assert.Contains(t, rebuiltNames(third), flagruntime.PublicName)

	// the last public flag set gets a key: nothing is public, the change says it was withdrawn
	fourth, err := b.Build(context.Background(), content(4, objs{
		"o1": flagsObj(
			5,
			"f",
		),
		"o2":     flagsObj(2, "k"),
		setsName: setsObj(t, fset(pubOne, src("o1"), keyB), fset("keyed", src("o2"), keyA)),
	}))
	require.NoError(t, err)
	assert.Nil(t, fourth.Runtime.Public)

	var withdrawn bool

	for _, item := range fourth.Rebuilt {
		if item.Name == flagruntime.PublicName {
			withdrawn = item.New.State == flagruntime.StateWithdrawn
		}
	}

	assert.True(t, withdrawn, "the public flag set is withdrawn")
}
