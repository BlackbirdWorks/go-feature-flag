package evaluate

import (
	"encoding/json"
	"time"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
	"github.com/blackbirdworks/semaphore/retriever"
)

const (
	// defaultPollingInterval is the polling interval of the retrievers of the command.
	defaultPollingInterval = 10 * time.Minute
)

type evaluate struct {
	retriever     retriever.Retriever
	fileFormat    string
	flag          string
	evaluationCtx string
}

// Evaluate evaluates the feature flags based on the configuration and context.
func (e evaluate) Evaluate() (map[string]model.RawVarResult, error) {
	goff, err := e.initGOFF()
	if err != nil {
		return nil, err
	}

	convertedEvaluationCtx, err := e.parseEvaluationContext()
	if err != nil {
		return nil, err
	}

	listFlags, err := e.getFlagList(goff)
	if err != nil {
		return nil, err
	}

	return e.evaluateFlags(goff, listFlags, convertedEvaluationCtx)
}

// initGOFF initializes the Semaphore client.
func (e evaluate) initGOFF() (*ffclient.GoFeatureFlag, error) {
	c := ffclient.Config{
		PollingInterval:       defaultPollingInterval,
		DisableNotifierOnInit: true,
		Retriever:             e.retriever,
		FileFormat:            e.fileFormat,
	}

	return ffclient.New(c)
}

// parseEvaluationContext parses the evaluation context from the command line arguments.
func (e evaluate) parseEvaluationContext() (ffcontext.EvaluationContext, error) {
	if e.evaluationCtx == "" {
		return ffcontext.NewEvaluationContextBuilder("").Build(), nil
	}

	var ctxAsMap map[string]any
	err := json.Unmarshal([]byte(e.evaluationCtx), &ctxAsMap)
	if err != nil {
		return ffcontext.EvaluationContext{}, err
	}

	targetingKey, ok := ctxAsMap["targetingKey"].(string)
	if !ok {
		targetingKey = ""
	}

	convertedEvaluationCtx := utils.ConvertEvaluationCtxFromRequest(targetingKey, ctxAsMap)

	return convertedEvaluationCtx, nil
}

// getFlagList gets the list of flags to evaluate.
func (e evaluate) getFlagList(goff *ffclient.GoFeatureFlag) ([]string, error) {
	if e.flag != "" {
		return []string{e.flag}, nil
	}

	flags, err := goff.GetFlagsFromCache()
	if err != nil {
		return nil, err
	}

	listFlags := make([]string, 0, len(flags))
	for key := range flags {
		listFlags = append(listFlags, key)
	}

	return listFlags, nil
}

// evaluateFlags evaluates the flags.
func (e evaluate) evaluateFlags(
	goff *ffclient.GoFeatureFlag,
	listFlags []string,
	convertedEvaluationCtx ffcontext.Context) (map[string]model.RawVarResult, error) {
	result := make(map[string]model.RawVarResult, len(listFlags))
	for _, flag := range listFlags {
		res, err := goff.RawVariation(flag, convertedEvaluationCtx, nil)
		if err != nil {
			return nil, err
		}
		result[flag] = res
	}

	return result, nil
}
