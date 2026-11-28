package flagruntime

import "github.com/blackbirdworks/semaphore/controlplane"

// Outcome is the result of one reconcile round.
type Outcome string

// The outcomes of a round.
const (
	// OutcomeApplied: a new Runtime was stored.
	OutcomeApplied Outcome = "applied"
	// OutcomeRejected: the candidate was invalid; nothing was stored.
	OutcomeRejected Outcome = "rejected"
	// OutcomeIgnored: the input was older than, or the same as, what runs; nothing was stored.
	OutcomeIgnored Outcome = "ignored"
)

// The codes of a [Reload].
const (
	CodeApplied            = "applied"
	CodeInvalidContent     = "invalid-content"
	CodeGenerationConflict = "generation-conflict"
	CodeStaleGeneration    = "stale-generation"
	CodeDuplicate          = "duplicate-generation"
	CodeInvalidBootstrap   = "invalid-bootstrap"
	CodeBuildFailed        = "build-failed"
	CodeVetoed             = "vetoed"
)

// CodeSourceDegraded is the code of a [Problem] about a source object that is not ok. The codes of the
// other problems are the controlplane codes ([controlplane.CodeUnresolvedSource], [controlplane.CodeDuplicateKey],
// [controlplane.CodeDuplicateName]) and the entry codes of its validation.
const CodeSourceDegraded controlplane.Code = "source-degraded"

// CodeDefinitionKept is the code of a [Problem] that says the flag set keeps its last valid definition because
// its entry in the new document is invalid.
const CodeDefinitionKept controlplane.Code = "definition-kept"

// Reload is the result of one round. It never contains an API key.
type Reload struct {
	// Outcome is the result.
	Outcome Outcome `json:"outcome"`
	// Code is the stable reason.
	Code string `json:"code"`
	// Detail says more, in words.
	Detail string `json:"detail"`
	// Generation is the generation the round was about (the attempted one for a rejection).
	Generation uint64 `json:"generation"`
	// Snapshot is the snapshot number stored by an applied round.
	Snapshot uint64 `json:"snapshot"`
}

// RoundResult is the result of one round as the engine reports it ([Engine.Result], [Hooks.AfterRound]): the
// [Reload] of the round itself, plus what the round did to the queue of change events. After an applied round the
// Reload is the one in Status.LastReload of the stored runtime; after a rejected or ignored round it is not (the
// running runtime keeps its previous LastReload).
type RoundResult struct {
	Reload

	// Events is the number of flag sets for which the round left a change event waiting in the pending table of the
	// engine, not yet handed to the channel of the consumer. A new change counts once, a change merged into a
	// waiting one counts once (per flag set and round), and a change that cancels out (a flag set created and
	// withdrawn, or flags gained and lost, while it is still in the pending table) counts zero. A round that is
	// not applied has none.
	Events int `json:"events"`
	// Revision is the revision of the snapshot whose content the round took, zero for a round without content. It is
	// the signal that a snapshot was decided: [Engine.Await] and the persistence of the applied state use it.
	Revision uint64 `json:"-"`
}
