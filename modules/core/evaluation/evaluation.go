package evaluation

// This package is moved from the modules/evaluation package to the modules/core package.
// It will be now part of the modules/core package.
// The old package is still available for backward compatibility, but will not be maintained anymore.
import (
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

var errWrongVariationUsed = errors.New("wrong variation used for flag")

// Evaluate evaluates the flag against the evaluation context and returns the variation result.
func Evaluate[T model.JSONType](
	targetFlag flagdef.Flag,
	flagKey string,
	evaluationCtx ffcontext.Context,
	flagCtx flagdef.Context,
	expectedType string,
	sdkDefaultValue T) (model.VariationResult[T], error) {
	flagValue, resolutionDetails := targetFlag.Value(flagKey, evaluationCtx, flagCtx)

	var convertedValue any

	switch value := flagValue.(type) {
	case float64:
		// this part ensures that we convert float64 value into int if we call IntVariation on a float64 value.
		if expectedType == "int" {
			convertedValue = int(value)
		} else {
			convertedValue = value
		}
	default:
		convertedValue = value
	}

	var typedValue T

	switch val := convertedValue.(type) {
	case T:
		typedValue = val
	default:
		if val != nil {
			return model.VariationResult[T]{
				Value:         sdkDefaultValue,
				VariationType: flagdef.VariationSDKDefault,
				Reason:        flagdef.ReasonError,
				ErrorCode:     flagdef.ErrorCodeTypeMismatch,
				ErrorDetails:  "",
				Failed:        true,
				TrackEvents:   targetFlag.IsTrackEvents(),
				Version:       targetFlag.GetVersion(),
				Metadata:      flagdef.CloneMetadata(targetFlag.GetMetadata()),
				Cacheable:     false,
				SegmentStatus: segment.StatusNone,
			}, fmt.Errorf("%w %v", errWrongVariationUsed, flagKey)
		}
	}

	return model.VariationResult[T]{
		Value:         typedValue,
		VariationType: resolutionDetails.Variant,
		Reason:        resolutionDetails.Reason,
		ErrorCode:     resolutionDetails.ErrorCode,
		ErrorDetails:  resolutionDetails.ErrorMessage,
		Failed:        resolutionDetails.ErrorCode != "",
		TrackEvents:   targetFlag.IsTrackEvents(),
		Version:       targetFlag.GetVersion(),
		Cacheable:     resolutionDetails.Cacheable,
		Metadata:      resolutionDetails.Metadata,
		SegmentStatus: resolutionDetails.SegmentStatus,
	}, nil
}
