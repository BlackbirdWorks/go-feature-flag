package evaluation

// This package is moved from the modules/evaluation package to the modules/core package.
// It will be now part of the modules/core package.
// The old package is still available for backward compatibility, but will not be maintained anymore.
import (
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/model"
)

var errWrongVariationUsed = errors.New("wrong variation used for flag")

func Evaluate[T model.JSONType](
	f flag.Flag,
	flagKey string,
	evaluationCtx ffcontext.Context,
	flagCtx flag.Context,
	expectedType string,
	sdkDefaultValue T) (model.VariationResult[T], error) {
	flagValue, resolutionDetails := f.Value(flagKey, evaluationCtx, flagCtx)
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

	var v T
	switch val := convertedValue.(type) {
	case T:
		v = val
	default:
		if val != nil {
			return model.VariationResult[T]{
				Value:         sdkDefaultValue,
				VariationType: flag.VariationSDKDefault,
				Reason:        flag.ReasonError,
				ErrorCode:     flag.ErrorCodeTypeMismatch,
				ErrorDetails:  "",
				Failed:        true,
				TrackEvents:   f.IsTrackEvents(),
				Version:       f.GetVersion(),
				Metadata:      f.GetMetadata(),
				Cacheable:     false,
			}, fmt.Errorf("%w %v", errWrongVariationUsed, flagKey)
		}
	}

	return model.VariationResult[T]{
		Value:         v,
		VariationType: resolutionDetails.Variant,
		Reason:        resolutionDetails.Reason,
		ErrorCode:     resolutionDetails.ErrorCode,
		ErrorDetails:  resolutionDetails.ErrorMessage,
		Failed:        resolutionDetails.ErrorCode != "",
		TrackEvents:   f.IsTrackEvents(),
		Version:       f.GetVersion(),
		Cacheable:     resolutionDetails.Cacheable,
		Metadata:      resolutionDetails.Metadata,
	}, nil
}
