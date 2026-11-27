package segmenttest_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
)

func seedOf(src *segment.MemorySource) segmenttest.Seed {
	return func(name string, ids []string) error {
		src.Set(name, ids)

		return nil
	}
}

//nolint:paralleltest // the suite measures the allocations of the whole process in one case
func TestMemorySourceSnapshotSuite(t *testing.T) {
	segmenttest.RunSnapshotSource(t, func() (segmenttest.SnapshotFixture, error) {
		src := segment.NewMemorySource(nil)

		return segmenttest.SnapshotFixture{Source: src, Seed: seedOf(src)}, nil
	})
}

func TestMemorySourceVersionerSuite(t *testing.T) {
	t.Parallel()

	segmenttest.RunVersioner(t, func() (segmenttest.VersionerFixture, error) {
		src := segment.NewMemorySource(nil)

		return segmenttest.VersionerFixture{Versioner: src, Seed: seedOf(src)}, nil
	})
}
