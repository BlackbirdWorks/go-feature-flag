package evaluate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/pkgs/evalcore"
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
func (e evaluate) Evaluate(ctx context.Context) (map[string]model.RawVarResult, error) {
	goff, err := e.initGOFF(ctx)
	if err != nil {
		return nil, err
	}

	convertedEvaluationCtx, err := e.parseEvaluationContext()
	if err != nil {
		return nil, err
	}

	listFlags, err := e.getFlagList(ctx, goff)
	if err != nil {
		return nil, err
	}

	return e.evaluateFlags(goff, listFlags, convertedEvaluationCtx)
}

// initGOFF initializes the Semaphore client.
func (e evaluate) initGOFF(ctx context.Context) (*ffclient.GoFeatureFlag, error) {
	cfg := ffclient.Config{
		PollingInterval:       defaultPollingInterval,
		DisableNotifierOnInit: true,
		Retriever:             e.retriever,
		FileFormat:            e.fileFormat,
	}

	goff, err := ffclient.NewWithContext(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("initializing the evaluation client: %w", err)
	}

	return goff, nil
}

// parseEvaluationContext parses the evaluation context from the command line arguments.
func (e evaluate) parseEvaluationContext() (ffcontext.EvaluationContext, error) {
	if e.evaluationCtx == "" {
		return ffcontext.NewEvaluationContextBuilder("").Build(), nil
	}

	var ctxAsMap map[string]any

	err := json.Unmarshal([]byte(e.evaluationCtx), &ctxAsMap)
	if err != nil {
		return ffcontext.EvaluationContext{}, fmt.Errorf("parsing the evaluation context: %w", err)
	}

	targetingKey, ok := ctxAsMap["targetingKey"].(string)
	if !ok {
		targetingKey = ""
	}

	convertedEvaluationCtx := evalcore.ConvertEvaluationCtxFromRequest(targetingKey, ctxAsMap)

	return convertedEvaluationCtx, nil
}

// getFlagList gets the list of flags to evaluate.
func (e evaluate) getFlagList(ctx context.Context, goff *ffclient.GoFeatureFlag) ([]string, error) {
	if e.flag != "" {
		return []string{e.flag}, nil
	}

	flags, err := goff.GetFlagsFromCacheWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting the flags of the configuration: %w", err)
	}

	listFlags := make([]string, 0, len(flags))
	for key := range flags {
		listFlags = append(listFlags, key)
	}

	return listFlags, nil
}

// evaluateFlags evaluates the flags.
func (evaluate) evaluateFlags(
	goff *ffclient.GoFeatureFlag,
	listFlags []string,
	convertedEvaluationCtx ffcontext.Context) (map[string]model.RawVarResult, error) {
	result := make(map[string]model.RawVarResult, len(listFlags))
	for _, flag := range listFlags {
		res, err := goff.RawVariation(flag, convertedEvaluationCtx, nil)
		if err != nil {
			return nil, fmt.Errorf("evaluating the flag: %w", err)
		}

		result[flag] = res
	}

	return result, nil
}
