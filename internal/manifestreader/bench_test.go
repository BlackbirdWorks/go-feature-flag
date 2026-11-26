//go:build bench

package manifestreader_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// benchRig starts a reader on generation 1 of 20000 objects and waits for its cold start.
func benchRig(b *testing.B) (*rig, []obj) {
	b.Helper()

	store := newFakeStore()
	objs := scaleObjects(controlplane.DefaultMaxObjects)
	publishJSON(b, store, 1, objs...)

	rdr := startRig(b, store, &manifestreader.Config{FetchConcurrency: manifestreader.MaxFetchConcurrency})
	require.NotNil(b, rdr.snapshot())

	return rdr, objs
}

// BenchmarkRound_NoChange is a round that costs the conditional GET and answers 304: nothing is decoded,
// fetched or published.
func BenchmarkRound_NoChange(b *testing.B) {
	rdr, _ := benchRig(b)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		rdr.round()
	}

	b.StopTimer()
	require.Equal(b, uint64(b.N), rdr.reader.Status().NotModified)
}

// BenchmarkRound_OneObjectChanged is a round that reads a new generation of 20000 entries (decode, validate,
// plan), fetches the one changed object and publishes the snapshot. Writing the manifest is not timed.
func BenchmarkRound_OneObjectChanged(b *testing.B) {
	rdr, objs := benchRig(b)

	b.ReportAllocs()
	b.ResetTimer()

	for i := range b.N {
		b.StopTimer()

		changed := append([]obj(nil), objs...)
		changed[len(changed)/2].content = fmt.Sprintf(`{"changed":%d}`, i)
		publishJSON(b, rdr.store, uint64(i)+2, changed...)

		b.StartTimer()

		calls := rdr.round()

		b.StopTimer()

		require.Len(b, calls, 2, "the manifest and the one changed object")
		require.NotNil(b, rdr.snapshot())

		b.StartTimer()
	}
}

// BenchmarkRound_NotModifiedWithAFailingObject is a 304 round while one of 20000 objects keeps failing: the
// round costs the conditional GET and the one failing object, and nothing that grows with the manifest.
func BenchmarkRound_NotModifiedWithAFailingObject(b *testing.B) {
	store := newFakeStore()
	objs := scaleObjects(controlplane.DefaultMaxObjects)
	publishJSON(b, store, 1, objs...)
	store.remove(keyOf(b, objs[len(objs)/2]))

	rdr := startRig(b, store, &manifestreader.Config{FetchConcurrency: manifestreader.MaxFetchConcurrency})
	require.NotNil(b, rdr.snapshot())
	require.Len(b, rdr.reader.Status().Failures, 1)

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		rdr.round()
	}

	b.StopTimer()
	require.Len(b, rdr.reader.Status().Failures, 1)
}
