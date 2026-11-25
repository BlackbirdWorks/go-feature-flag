package ffclient

import (
	"context"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// GetFlagStates is evaluating all the flags in flagsToEvaluate based on the context provided.
// If flagsToEvaluate is nil or empty, it will evaluate all the flags available in Semaphore.
func (g *GoFeatureFlag) GetFlagStates(
	evaluationCtx ffcontext.Context,
	flagsToEvaluate []string,
) flagstate.AllFlags {
	if g == nil {
		return flagstate.AllFlags{}
	}
	if g.config.Offline {
		return flagstate.NewAllFlags()
	}

	// prepare evaluation context enrichment
	flagCtx := flag.Context{
		EvaluationContextEnrichment: g.evaluationEnrichment(),
		DefaultSdkValue:             nil,
		Service:                     g.evaluationService,
	}

	// Evaluate only the flags in flagsToEvaluate
	if len(flagsToEvaluate) != 0 {
		flagStates := flagstate.NewAllFlags()
		for _, key := range flagsToEvaluate {
			currentFlag, err := g.retrieverManager.GetFlag(key)
			if err != nil {
				// We ignore flags in error
				continue
			}
			flagStates.AddFlag(
				key,
				flagstate.FromFlagEvaluation(key, evaluationCtx, flagCtx, currentFlag),
			)
		}

		return flagStates
	}

	// Evaluate all the flags
	// the flags are only read to be evaluated, so they are not copied.
	flags, err := g.GetFlagsFromCacheReadOnly()
	if err != nil {
		return flagstate.AllFlags{}
	}
	allFlags := flagstate.NewAllFlags()
	for key, currentFlag := range flags {
		allFlags.AddFlag(
			key,
			flagstate.FromFlagEvaluation(key, evaluationCtx, flagCtx, currentFlag),
		)
	}

	return allFlags
}

// AllFlagsState return a flagstate.AllFlags that contains all the flags for a specific user.
func (g *GoFeatureFlag) AllFlagsState(evaluationCtx ffcontext.Context) flagstate.AllFlags {
	if g == nil {
		return flagstate.AllFlags{}
	}

	return g.GetFlagStates(evaluationCtx, []string{})
}

// GetFlagsFromCache returns all the flags present in the cache with their
// current state when calling this method. If cache hasn't been initialized, an
// error reporting this is returned.
func (g *GoFeatureFlag) GetFlagsFromCache() (map[string]flag.Flag, error) {
	return g.GetFlagsFromCacheWithContext(context.Background())
}

// GetFlagsFromCacheReadOnly is GetFlagsFromCache without the deep copy of the flags: the
// flags are the ones the evaluations use. The caller must only read them (evaluate them,
// look at them) and must not change them, nor hand them to code that does.
// It is the cheap way to evaluate every flag again and again, a copy rebuilds the state that
// is memoized for the scheduled rollouts.
func (g *GoFeatureFlag) GetFlagsFromCacheReadOnly() (map[string]flag.Flag, error) {
	//nolint:wrapcheck // preserves retriever.Manager cache sentinel errors
	return g.retrieverManager.GetFlagsFromCacheReadOnly(context.Background())
}

// GetFlagsFromCacheWithContext returns all the flags present in the cache with their
// current state when calling this method. If cache hasn't been initialized, an
// error reporting this is returned. The provided context is used for the operation.
func (g *GoFeatureFlag) GetFlagsFromCacheWithContext(ctx context.Context) (map[string]flag.Flag, error) {
	//nolint:wrapcheck // preserves retriever.Manager cache sentinel errors
	return g.retrieverManager.GetFlagsFromCache(ctx)
}
