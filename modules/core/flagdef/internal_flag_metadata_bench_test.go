package flagdef_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// BenchmarkInternalFlagValue_DisabledWithMetadata measures the disabled path of a flag that has a metadata block:
// the metadata that is returned is a copy the caller owns, so this path allocates it.
func BenchmarkInternalFlagValue_DisabledWithMetadata(b *testing.B) {
	flg := &flagdef.InternalFlag{
		Variations:  &map[string]*any{"on": new(any("on"))},
		DefaultRule: &flagdef.Rule{VariationResult: new("on")},
		Disable:     new(true),
		Metadata:    &map[string]any{"owner": "team-a", "description": "a flag"},
	}
	flagContext := flagdef.Context{Service: flagdef.NewService()}
	evalCtx := ffcontext.NewEvaluationContext("user")

	b.ReportAllocs()

	for b.Loop() {
		_, _ = flg.Value("flag", evalCtx, flagContext)
	}
}
