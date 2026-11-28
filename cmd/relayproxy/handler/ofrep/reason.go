package ofrep

import "github.com/blackbirdworks/semaphore/modules/core/flagdef"

// ofrepReason maps an internal engine resolution reason to the OFREP 0.4.0
// evaluationSuccess.properties.reason enum (STATIC, TARGETING_MATCH, SPLIT, DISABLED, UNKNOWN).
func ofrepReason(reason flagdef.ResolutionReason) string {
	switch reason {
	case flagdef.ReasonStatic, flagdef.ReasonDefault:
		return flagdef.ReasonStatic
	case flagdef.ReasonTargetingMatch:
		return flagdef.ReasonTargetingMatch
	case flagdef.ReasonSplit, flagdef.ReasonTargetingMatchSplit:
		return flagdef.ReasonSplit
	case flagdef.ReasonDisabled:
		return flagdef.ReasonDisabled
	default:
		// ReasonUnknown, ReasonOffline, ReasonError, and any unrecognized reason map to UNKNOWN.
		return flagdef.ReasonUnknown
	}
}

// ofrepBulkReason maps an engine resolution reason for a bulk evaluation item.
// Failed bulk items serialize as evaluationFailure (matching on errorCode), so ReasonError stays ERROR.
func ofrepBulkReason(reason flagdef.ResolutionReason) string {
	if reason == flagdef.ReasonError {
		return flagdef.ReasonError
	}

	return ofrepReason(reason)
}
