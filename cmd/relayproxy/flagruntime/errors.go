package flagruntime

import "errors"

var (
	// ErrInvalidContent is matched by a rejection of a Content that is malformed (generation 0, an unknown
	// object state or kind of state, an unsupported format).
	ErrInvalidContent = errors.New("runtime: invalid content")
	// ErrGenerationConflict is matched by the rejection of a Content with the generation that is running
	// but another content.
	ErrGenerationConflict = errors.New("runtime: generation conflict")
	// ErrStaleGeneration is matched when a Content has a lower generation than the running one, or the same
	// content again; it is ignored.
	ErrStaleGeneration = errors.New("runtime: stale generation")
	// ErrInvalidBootstrap is matched by the rejection of a bootstrap configuration that cannot be applied
	// (an admin key that does not resolve).
	ErrInvalidBootstrap = errors.New("runtime: invalid bootstrap")
	// ErrVetoed is matched when the BeforeSwap hook refused the candidate.
	ErrVetoed = errors.New("runtime: candidate vetoed")
	// ErrDecode is matched by the reason of an object that cannot be decoded.
	ErrDecode = errors.New("runtime: decode")
	// ErrDynamicDefinitions is matched by the reason of a segments object that holds a dynamic definition that its
	// connections cannot serve: the connection is not in the bootstrap file, cannot serve segments, or cannot serve
	// the mode.
	ErrDynamicDefinitions = errors.New("runtime: the dynamic segments do not fit the connections of the bootstrap file")
)
