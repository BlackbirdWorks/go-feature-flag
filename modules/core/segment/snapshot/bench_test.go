package snapshot_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

func BenchmarkLookup(b *testing.B) {
	ids := make([]string, 100_000)
	for i := range ids {
		ids[i] = "member-" + strconv.Itoa(i)
	}

	holder, err := snapshot.New(
		segAlpha,
		segment.NewMemorySource(map[string][]string{segAlpha: ids}),
		snapshot.Options{},
	)
	require.NoError(b, err)
	require.NoError(b, holder.Refresh(context.Background()))

	b.ReportAllocs()

	var sink segment.State

	for i := 0; b.Loop(); i++ {
		sink = holder.Lookup(ids[i%len(ids)])
	}

	require.Equal(b, segment.Member, sink)
}
