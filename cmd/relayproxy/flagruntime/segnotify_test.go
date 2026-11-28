package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/notifier"
)

const editedFlagYAML = `
edited-flag:
  variations: {on: true, off: false}
  metadata: {updatedBy: jane}
  targeting:
    - {name: members, query: 'targetingKey in segment("beta")', variation: on}
  defaultRule: {name: default, variation: off}
plain-edited-flag:
  variations: {on: true, off: false}
  metadata: {updatedBy: john}
  defaultRule: {name: default, variation: off}
`

// The notifiers read the editor of a flag in the change events: a new flag that uses segments is wrapped by the engine
// (it carries the signature of its segments), and the editor is still found, as for a flag that uses none.
func TestNotifierSeesTheEditorOfANewFlagThatUsesSegments(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	harness.feed(content(1, objs{
		"f1":      obj(flagruntime.KindFlags, []byte(editedFlagYAML)),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(docBetaUnused),
		setsName:  withSegmentSources(t),
	}))

	change := harness.nextChange()
	require.Equal(t, "a", change.FlagSet)

	tests := []struct {
		name       string
		flag       string
		wantEditor string
		wantSuffix string
	}{
		{name: "uses segments", flag: "edited-flag", wantEditor: "jane", wantSuffix: " by jane"},
		{name: "uses none", flag: "plain-edited-flag", wantEditor: "john", wantSuffix: " by john"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			added, ok := change.Diff.Added[tt.flag]
			require.True(t, ok)
			assert.Equal(t, tt.wantEditor, notifier.UpdatedBy(added))
			assert.Equal(t, tt.wantSuffix, notifier.UpdatedBySuffix(added))
		})
	}
}

// A change of the members of a segment is an update of the flags that use it: the notifiers describe it (no line, the
// flag itself did not change) instead of failing on the wrapped flags.
func TestNotifierDescribesAnUpdateOfAFlagWhoseSegmentChanged(t *testing.T) {
	t.Parallel()

	harness := newRig(t, flagruntime.Hooks{})
	harness.start()

	all := objs{
		"f1":      obj(flagruntime.KindFlags, []byte(editedFlagYAML)),
		"f2":      flagsObj(1, "z"),
		segObject: segObj(docBetaUnused),
		setsName:  withSegmentSources(t),
	}

	harness.feed(content(1, all))
	harness.nextChange()
	harness.nextChange()

	harness.feed(content(2, all.with(objs{segObject: segObj(docBetaTwo)})))

	change := harness.nextChange()
	update, ok := change.Diff.Updated["edited-flag"]
	require.True(t, ok)

	lines, err := notifier.DescribeChanges(update.Before, update.After)
	require.NoError(t, err)
	assert.Empty(t, lines)
	assert.Equal(t, "jane", notifier.UpdatedBy(update.After))
}
