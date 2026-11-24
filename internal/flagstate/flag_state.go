package flagstate

import (
	"time"

	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// FlagState represents the state of an individual feature flag, with regard to a specific user, when it was called.
type FlagState struct {
	Value         any                   `json:"value"`
	Timestamp     int64                 `json:"timestamp"`
	VariationType string                `json:"variationType"`
	TrackEvents   bool                  `json:"trackEvents"`
	Failed        bool                  `json:"-"`
	ErrorCode     flag.ErrorCode        `json:"errorCode"`
	ErrorDetails  string                `json:"errorDetails,omitempty"`
	Reason        flag.ResolutionReason `json:"reason"`
	Metadata      map[string]any        `json:"metadata,omitempty"`
}

// FromFlagEvaluation builds the state of a flag evaluated now.
func FromFlagEvaluation(key string, evaluationCtx ffcontext.Context,
	flagCtx flag.Context, currentFlag flag.Flag) FlagState {
	return FromFlagEvaluationAt(clock.Now(), key, evaluationCtx, flagCtx, currentFlag)
}

// FromFlagEvaluationAt builds the state of a flag evaluated at the given instant.
func FromFlagEvaluationAt(at time.Time, key string, evaluationCtx ffcontext.Context,
	flagCtx flag.Context, currentFlag flag.Flag) FlagState {
	flagValue, resolutionDetails := currentFlag.Value(key, evaluationCtx, flagCtx)

	// if the flag is disabled, we are ignoring it.
	if resolutionDetails.Reason == flag.ReasonDisabled {
		return FlagState{
			Timestamp:    at.Unix(),
			TrackEvents:  currentFlag.IsTrackEvents(),
			Failed:       resolutionDetails.ErrorCode != "",
			ErrorCode:    resolutionDetails.ErrorCode,
			ErrorDetails: resolutionDetails.ErrorMessage,
			Reason:       resolutionDetails.Reason,
			Metadata:     resolutionDetails.Metadata,
		}
	}

	if resolutionDetails.Reason == flag.ReasonError {
		return FlagState{
			Timestamp:    at.Unix(),
			TrackEvents:  currentFlag.IsTrackEvents(),
			Failed:       resolutionDetails.ErrorCode != "",
			ErrorCode:    resolutionDetails.ErrorCode,
			ErrorDetails: resolutionDetails.ErrorMessage,
			Reason:       resolutionDetails.Reason,
			Metadata:     resolutionDetails.Metadata,
		}
	}

	switch v := flagValue; v.(type) {
	case int, float64, bool, string, []any, map[string]any:
		return FlagState{
			Value:         v,
			Timestamp:     at.Unix(),
			VariationType: resolutionDetails.Variant,
			TrackEvents:   currentFlag.IsTrackEvents(),
			Failed:        resolutionDetails.ErrorCode != "",
			ErrorCode:     resolutionDetails.ErrorCode,
			ErrorDetails:  resolutionDetails.ErrorMessage,
			Reason:        resolutionDetails.Reason,
			Metadata:      resolutionDetails.Metadata,
		}

	default:
		defaultVariationName := flag.VariationSDKDefault
		defaultVariationValue := currentFlag.GetVariationValue(defaultVariationName)

		return FlagState{
			Value:         defaultVariationValue,
			Timestamp:     at.Unix(),
			VariationType: defaultVariationName,
			TrackEvents:   currentFlag.IsTrackEvents(),
			Failed:        true,
			ErrorCode:     flag.ErrorCodeTypeMismatch,
			ErrorDetails:  resolutionDetails.ErrorMessage,
			Reason:        flag.ReasonError,
			Metadata:      resolutionDetails.Metadata,
		}
	}
}
