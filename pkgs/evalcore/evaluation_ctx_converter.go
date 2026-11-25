package evalcore

import "github.com/blackbirdworks/semaphore/modules/core/ffcontext"

// ConvertEvaluationCtxFromRequest convert the result of an unmarshal request from the API to a
// ffcontext.EvaluationContext.
func ConvertEvaluationCtxFromRequest(
	targetingKey string,
	custom map[string]any,
) ffcontext.EvaluationContext {
	evalCtx := ffcontext.NewEvaluationContextBuilder(targetingKey)

	for key, rawVal := range custom {
		switch val := rawVal.(type) {
		case float64:
			if IsIntegral(val) {
				evalCtx.AddCustom(key, int(val))

				continue
			}

			evalCtx.AddCustom(key, val)
		default:
			evalCtx.AddCustom(key, val)
		}
	}

	return evalCtx.Build()
}
