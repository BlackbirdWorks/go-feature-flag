package utils_test

import (
	"fmt"
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

func BenchmarkContextToMap(b *testing.B) {
	for _, count := range []int{0, 5, 15} {
		builder := ffcontext.NewEvaluationContextBuilder("3f1c1a52-8a5e-4c1e-9d0a-2b7f6c2e9a10")
		for i := range count {
			builder.AddCustom(fmt.Sprintf("attr%d", i), i)
		}
		ctx := builder.Build()
		b.Run(fmt.Sprintf("%d custom attributes", count), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = utils.ContextToMap(ctx)
			}
		})
	}
}
