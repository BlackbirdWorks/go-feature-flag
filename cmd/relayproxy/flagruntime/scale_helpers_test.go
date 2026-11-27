package flagruntime_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	scaleFlagsPerSet   = 100
	scaleSetsPerObject = 10
)

// scaleObjectName, scaleSetName and scaleKey name the parts of a generated fleet.
func scaleObjectName(idx int) string { return fmt.Sprintf("obj-%04d", idx) }

func scaleSetName(obj, slot int) string { return fmt.Sprintf("set-%04d-%d", obj, slot) }

func scaleKey(obj, slot int) string { return "key-" + scaleSetName(obj, slot) }

// scaleValue is the value that every flag of the object idx carries in generation gen; it identifies both.
func scaleValue(gen uint64, idx int) int { return int(gen)*1_000_000 + idx }

// scaleFleet generates, in memory, sets flag sets of flagsPerSet flags. scaleSetsPerObject flag sets share one flags
// object and every object has its own content. The flags of the object idx carry valueOf(idx).
func scaleFleet(
	tb testing.TB, gen uint64, sets, flagsPerSet int, valueOf func(idx int) int,
) *flagruntime.Content {
	tb.Helper()

	names := make([]string, flagsPerSet)
	for i := range names {
		names[i] = fmt.Sprintf("flag-%03d", i)
	}

	objects := objs{}
	flagSets := make([]controlplane.FlagSet, 0, sets)

	for idx := range sets / scaleSetsPerObject {
		objects[scaleObjectName(idx)] = flagsObj(valueOf(idx), names...)

		for slot := range scaleSetsPerObject {
			flagSets = append(flagSets, fset(scaleSetName(idx, slot), src(scaleObjectName(idx)), scaleKey(idx, slot)))
		}
	}

	objects[setsName] = setsObj(tb, flagSets...)

	return content(gen, objects)
}

// scaleGeneration is a fleet whose objects all carry the values of generation gen.
func scaleGeneration(tb testing.TB, gen uint64, sets int) *flagruntime.Content {
	tb.Helper()

	return scaleFleet(tb, gen, sets, scaleFlagsPerSet, func(idx int) int { return scaleValue(gen, idx) })
}

// scaleVariant is generation 1 with the flags of the listed objects changed.
func scaleVariant(tb testing.TB, sets int, changed ...int) *flagruntime.Content {
	tb.Helper()

	return scaleFleet(tb, 1, sets, scaleFlagsPerSet, func(idx int) int {
		if slices.Contains(changed, idx) {
			return scaleValue(2, idx)
		}

		return scaleValue(1, idx)
	})
}

// scaleSetNames lists the names of the flag sets that use the given objects, sorted.
func scaleSetNames(objects ...int) []string {
	out := make([]string, 0, len(objects)*scaleSetsPerObject)

	for _, idx := range objects {
		for slot := range scaleSetsPerObject {
			out = append(out, scaleSetName(idx, slot))
		}
	}

	return sortedStrings(out)
}

// buildCommitted builds a content and makes it the base of the builder.
func buildCommitted(tb testing.TB, builder *flagruntime.Builder, in *flagruntime.Content) *flagruntime.Built {
	tb.Helper()

	built, err := builder.Build(context.Background(), in)
	require.NoError(tb, err)

	builder.Commit(built)

	return built
}
