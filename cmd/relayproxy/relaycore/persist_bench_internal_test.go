//go:build bench

package relaycore

import (
	"fmt"
	"testing"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// bigSnapshot is a manifest of n flags objects with distinct checksums, and the status of a runtime that holds them.
func bigSnapshot(n int) (manifestreader.Persist, map[string]*flagruntime.ObjectStatus) {
	objects := make(map[string]manifestreader.ObjectState, n)
	status := make(map[string]*flagruntime.ObjectStatus, n)

	for i := range n {
		name, sum := fmt.Sprintf("obj-%d", i), fmt.Sprintf("sum-%d", i)
		objects[name] = *objectOf(controlplane.KindFlags, sum)
		status[name] = &flagruntime.ObjectStatus{
			Name:     name,
			Kind:     controlplane.KindFlags,
			State:    flagruntime.ObjectOK,
			Checksum: sum,
		}
	}

	return manifestreader.Persist{
		Manifest: []byte("manifest"), Snapshot: &manifestreader.Snapshot{Revision: 1, Generation: 1, Objects: objects},
	}, status
}

// BenchmarkPersisterServed is the cost of what is handed to the state store for a manifest of 20,000 objects.
func BenchmarkPersisterServed(b *testing.B) {
	persist, status := bigSnapshot(20000)

	b.ReportAllocs()

	for b.Loop() {
		p := newPersister(func(manifestreader.Persist) {}, never)
		_ = p.served(persist.Snapshot, status)
	}
}
