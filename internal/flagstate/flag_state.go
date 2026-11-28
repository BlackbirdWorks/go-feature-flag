package flagstate

import (
	"time"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// FlagState represents the state of an individual feature flag, with regard to a specific user, when it was called.
type FlagState struct { //nolint:govet // fieldalignment: field order is the key order of the CLI and API output
	Value         any                      `json:"value"`
	Timestamp     int64                    `json:"timestamp"`
	VariationType string                   `json:"variationType"`
	TrackEvents   bool                     `json:"trackEvents"`
	Failed        bool                     `json:"-"`
	ErrorCode     flagdef.ErrorCode        `json:"errorCode"`
	ErrorDetails  string                   `json:"errorDetails,omitempty"`
	Reason        flagdef.ResolutionReason `json:"reason"`
	Metadata      map[string]any           `json:"metadata,omitempty"`
	// SegmentStatus is the health of the segment lookups; it is not part of the JSON, see [WithSegmentStatus].
	SegmentStatus segment.Status `json:"-"`
}

// MetadataSegmentStatus is the response metadata key of the health of the segment lookups of an evaluation.
const MetadataSegmentStatus = "semaphore_segmentStatus"

// WithSegmentStatus adds the MetadataSegmentStatus key to the metadata of an evaluation whose segment lookups were
// not healthy (STALE, NOT_CONFIGURED or STORE_ERROR). A healthy evaluation, and one with no segment, get the
// metadata back untouched: no copy, no allocation. The map belongs to the evaluation, so the key is added to it.
func WithSegmentStatus(metadata map[string]any, status segment.Status) map[string]any {
	if !status.Reported() {
		return metadata
	}

	if metadata == nil {
		metadata = make(map[string]any, 1)
	}

	metadata[MetadataSegmentStatus] = status.String()

	return metadata
}

// FromFlagEvaluation builds the state of a flag evaluated now.
func FromFlagEvaluation(key string, evaluationCtx ffcontext.Context,
	flagCtx flagdef.Context, currentFlag flagdef.Flag) FlagState {
	return FromFlagEvaluationAt(clock.Now(), key, evaluationCtx, flagCtx, currentFlag)
}

// FromFlagEvaluationAt builds the state of a flag evaluated at the given instant.
func FromFlagEvaluationAt(evaluatedAt time.Time, key string, evaluationCtx ffcontext.Context,
	flagCtx flagdef.Context, currentFlag flagdef.Flag) FlagState {
	flagValue, resolutionDetails := currentFlag.Value(key, evaluationCtx, flagCtx)

	// if the flag is disabled, we are ignoring it.
	if resolutionDetails.Reason == flagdef.ReasonDisabled {
		return FlagState{
			Value:         nil,
			Timestamp:     evaluatedAt.Unix(),
			VariationType: "",
			TrackEvents:   currentFlag.IsTrackEvents(),
			Failed:        resolutionDetails.ErrorCode != "",
			ErrorCode:     resolutionDetails.ErrorCode,
			ErrorDetails:  resolutionDetails.ErrorMessage,
			Reason:        resolutionDetails.Reason,
			Metadata:      resolutionDetails.Metadata,
			SegmentStatus: resolutionDetails.SegmentStatus,
		}
	}

	if resolutionDetails.Reason == flagdef.ReasonError {
		return FlagState{
			Value:         nil,
			Timestamp:     evaluatedAt.Unix(),
			VariationType: "",
			TrackEvents:   currentFlag.IsTrackEvents(),
			Failed:        resolutionDetails.ErrorCode != "",
			ErrorCode:     resolutionDetails.ErrorCode,
			ErrorDetails:  resolutionDetails.ErrorMessage,
			Reason:        resolutionDetails.Reason,
			Metadata:      resolutionDetails.Metadata,
			SegmentStatus: resolutionDetails.SegmentStatus,
		}
	}

	switch v := flagValue; v.(type) {
	case int, float64, bool, string, []any, map[string]any:
		return FlagState{
			Value:         v,
			Timestamp:     evaluatedAt.Unix(),
			VariationType: resolutionDetails.Variant,
			TrackEvents:   currentFlag.IsTrackEvents(),
			Failed:        resolutionDetails.ErrorCode != "",
			ErrorCode:     resolutionDetails.ErrorCode,
			ErrorDetails:  resolutionDetails.ErrorMessage,
			Reason:        resolutionDetails.Reason,
			Metadata:      resolutionDetails.Metadata,
			SegmentStatus: resolutionDetails.SegmentStatus,
		}

	default:
		defaultVariationName := flagdef.VariationSDKDefault
		defaultVariationValue := currentFlag.GetVariationValue(defaultVariationName)

		return FlagState{
			Value:         defaultVariationValue,
			Timestamp:     evaluatedAt.Unix(),
			VariationType: defaultVariationName,
			TrackEvents:   currentFlag.IsTrackEvents(),
			Failed:        true,
			ErrorCode:     flagdef.ErrorCodeTypeMismatch,
			ErrorDetails:  resolutionDetails.ErrorMessage,
			Reason:        flagdef.ReasonError,
			Metadata:      resolutionDetails.Metadata,
			SegmentStatus: resolutionDetails.SegmentStatus,
		}
	}
}
