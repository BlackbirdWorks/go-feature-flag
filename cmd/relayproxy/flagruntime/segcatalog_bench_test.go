//go:build bench

package flagruntime_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
)

// BenchmarkMembershipOnlyReload is the cost of a build when a segments object changes its members and nothing else:
// the flag set that lists it is rebuilt (catalog, signatures of the flags that use segments), and the other flag
// sets are carried over. The flags objects are not read again.
func BenchmarkMembershipOnlyReload(b *testing.B) {
	for _, flags := range []int{100, 5000} {
		b.Run(fmt.Sprintf("%d flags using the segment", flags), func(b *testing.B) {
			var yaml strings.Builder

			for i := range flags {
				fmt.Fprintf(&yaml, "f%d:\n  variations: {on: true, off: false}\n  targeting:\n"+
					"    - {name: r, query: 'targetingKey in segment(\"beta\")', variation: on}\n"+
					"  defaultRule: {variation: off}\n", i)
			}

			svc := newService(b)
			builder := flagruntime.NewBuilder(svc, flagruntime.BuilderOptions{})
			base := objs{
				"f1":      obj(flagruntime.KindFlags, []byte(yaml.String())),
				"f2":      flagsObj(1, "z"),
				segObject: segObj(docBetaUnused),
				setsName:  withSegmentSources(b),
			}

			built, err := builder.Build(b.Context(), content(1, base))
			require.NoError(b, err)
			builder.Commit(built)

			docs := [2]string{docBetaUnused, docBetaTwo} // the same segment names: only the members change

			b.ReportAllocs()
			b.ResetTimer()

			for i := range b.N {
				built, err = builder.Build(
					b.Context(),
					content(uint64(i+2), base.with(objs{segObject: segObj(docs[i%2])})),
				)
				require.NoError(b, err)

				builder.Commit(built)
			}
		})
	}
}
