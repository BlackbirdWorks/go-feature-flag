package flag

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/diegoholiveira/jsonlogic/v3"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/internalerror"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
	"github.com/blackbirdworks/semaphore/rules/parser"
)

const (
	// maxPercentage is the percentage that selects every user.
	maxPercentage = 100
)

var (
	errEvaluateRuleNoKey           = errors.New("evaluate Rule: no key for bucketing-required rule")
	errEvaluateRuleNoEvaluation    = errors.New("evaluate Rule: no evaluation context")
	errProgressiveRolloutRequiresA = errors.New("progressive rollout requires a bucketing key")
	errPercentageRolloutRequiresA  = errors.New("percentage rollout requires a bucketing key")
	errErrorInTheConfiguration     = errors.New("error in the configuration, no variation available for this rule")
	errErrorInTheProgressive       = errors.New("error in the progressive rollout, missing params")
	errImpossibleToFindThe         = errors.New("impossible to find the variation")
)

type QueryFormat = string

const (
	NikunjyQueryFormat   QueryFormat = "nikunjy"
	JSONLogicQueryFormat QueryFormat = "jsonlogic"
)

// Rule represents a rule applied by the flag.
type Rule struct {
	// Name is the name of the rule, this field is mandatory if you want
	// to update the rule during scheduled rollout
	Name *string `json:"name,omitempty" yaml:"name,omitempty" toml:"name,omitempty" jsonschema:"title=name,description=Name is the name of the rule. This field is mandatory if you want to update the rule during scheduled rollout."` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Query represents the query used to target the audience of the flag.
	Query *string `json:"query,omitempty" yaml:"query,omitempty" toml:"query,omitempty" jsonschema:"title=query,description=The query that allow to check in the evaluation context match. Note: in the defaultRule field query is ignored."` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// VariationResult represents the variation name to use if the rule apply for the user.
	// In case we have a percentage field in the config VariationResult is ignored
	VariationResult *string `json:"variation,omitempty" yaml:"variation,omitempty" toml:"variation,omitempty" jsonschema:"title=variation,description=The variation name to use if the rule apply for the user. In case we have a percentage field in the config this field is ignored"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Percentages represents the percentage we should give to each variation.
	// example: variationA = 10%, variationB = 80%, variationC = 10%
	Percentages *map[string]float64 `json:"percentage,omitempty" yaml:"percentage,omitempty" toml:"percentage,omitempty" jsonschema:"title=percentage,description=Represents the percentage we should give to each variation."` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// ProgressiveRollout is your struct to configure a progressive rollout deployment of your flag.
	// It will allow you to ramp up the percentage of your flag over time.
	// You can decide at which percentage you starts with and at what percentage you ends with in your release ramp.
	// Before the start date we will serve the initial percentage and, after we will serve the end percentage.
	ProgressiveRollout *ProgressiveRollout `json:"progressiveRollout,omitempty" yaml:"progressiveRollout,omitempty" toml:"progressiveRollout,omitempty" jsonschema:"title=progressiveRollout,description=Configure a progressive rollout deployment of your flag."` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Disable indicates that this rule is disabled.
	Disable *bool `json:"disable,omitempty" yaml:"disable,omitempty" toml:"disable,omitempty" jsonschema:"title=disable,description=Indicates that this rule is disabled."` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// QueryHash is the hash of Query that InternalFlag.IsValid computes once, when the flag is
	// loaded, to find the evaluator of the query without hashing it for every evaluation.
	// It is derived data: it is never read from or written to a configuration. It is exported
	// only because the notifier compares flags with cmp.Equal, which rejects unexported
	// fields; flags with the same queries have the same hash, so it does not make flags differ.
	QueryHash uint64 `json:"-" yaml:"-" toml:"-" jsonschema:"-"`
}

// RequiresBucketing checks if this rule requires a bucketing key for evaluation.
func (r *Rule) RequiresBucketing() bool {
	// Rules with percentages need bucketing for distribution
	if r.Percentages != nil && len(r.GetPercentages()) > 0 {
		return true
	}

	// Rules with progressive rollout need bucketing
	if r.ProgressiveRollout != nil {
		return true
	}

	return false
}

// Evaluate is checking if the rule applies to for the user.
// If yes, it returns the variation you should use for this rule. The queries are parsed and cached
// by svc, which is required.
func (r *Rule) Evaluate(svc *Service, key string, ctx ffcontext.Context, flagName string, isDefault bool,
) (string, error) {
	if svc == nil {
		return "", errNoService
	}
	v, err := r.evaluate(key, &queryContext{svc: svc, ctx: ctx}, flagName, isDefault)
	if errors.Is(err, errRuleNotApply) {
		return "", &internalerror.RuleNotApplyError{Context: ctx}
	}

	return v, err
}

// errRuleNotApply is returned by evaluate when the rule does not apply to the context.
// It is a sentinel to avoid allocating a RuleNotApplyError for every non-matching rule.
var errRuleNotApply = errors.New("rule does not apply")

// evaluate is Evaluate reading the context through queryCtx, which the caller can share
// across the rules of a flag.
func (r *Rule) evaluate(key string, queryCtx *queryContext, flagName string, isDefault bool,
) (string, error) {
	ctx := queryCtx.ctx
	// Only require key if this rule needs bucketing
	if key == "" && r.RequiresBucketing() {
		return "", errEvaluateRuleNoKey
	}

	// check that we have an evaluation context
	if ctx == nil {
		return "", errEvaluateRuleNoEvaluation
	}

	// Check if the rule applies for this user
	ruleApply := isDefault
	if !ruleApply {
		ruleApply = r.evaluateQuery(queryCtx)
	}
	if !ruleApply || (!isDefault && r.IsDisable()) {
		return "", errRuleNotApply
	}
	if r.ProgressiveRollout != nil {
		if key == "" {
			return "", errProgressiveRolloutRequiresA
		}

		// only the progressive rollout depends on the date: the clock is not read for the other rules.
		return r.EvaluateProgressiveRollout(key, flagName, queryCtx.svc.evaluationDate(ctx))
	}
	if r.Percentages != nil && len(r.GetPercentages()) > 0 {
		if key == "" {
			return "", errPercentageRolloutRequiresA
		}

		return r.EvaluatePercentageRollout(key, flagName)
	}
	if r.VariationResult != nil {
		return r.GetVariationResult(), nil
	}

	return "", errErrorInTheConfiguration
}

// queryContext is the evaluation context as targeting queries read it. The map is built on
// first use and shared by every rule of a flag, so a flag with several targeting rules
// converts the context once per evaluation instead of once per rule. Because it is shared,
// the query engines must only read the map, never modify it. The JSON form JsonLogic reads
// is shared the same way.
type queryContext struct {
	// svc owns the caches and the pool that evaluating the queries uses.
	svc                  *Service
	ctx                  ffcontext.Context
	parsedEvalCtxAsMap   map[string]any
	parsedEvalCtxAsBytes []byte
	parsedEvalCtxAsData  any
	// pooled is true when parsedEvalCtxAsMap comes from the pool of svc (see release).
	pooled bool
}

func (q *queryContext) mapOnce() map[string]any {
	if q.parsedEvalCtxAsMap == nil {
		pooled := q.svc.queryMaps.Get().(map[string]any) //nolint:errcheck // the pool only holds map[string]any
		q.parsedEvalCtxAsMap = utils.ContextToMapInto(pooled, q.ctx)
		q.pooled = true
	}

	return q.parsedEvalCtxAsMap
}

// release gives the map back to the pool. The queryContext must not be used afterwards.
func (q *queryContext) release() {
	if q.pooled {
		clear(q.parsedEvalCtxAsMap)
		q.svc.queryMaps.Put(q.parsedEvalCtxAsMap)
		q.parsedEvalCtxAsMap = nil
		q.pooled = false
	}
}

func (q *queryContext) jsonOnce() ([]byte, error) {
	if q.parsedEvalCtxAsBytes == nil {
		var err error
		if q.parsedEvalCtxAsBytes, err = json.Marshal(q.mapOnce()); err != nil {
			return nil, err
		}
	}

	return q.parsedEvalCtxAsBytes, nil
}

// dataOnce is the context decoded from its JSON form, which is the only shape JsonLogic accepts.
func (q *queryContext) dataOnce() (any, error) {
	if q.parsedEvalCtxAsData == nil {
		// Common case: the context only has JSON-like values, so what the encoding would
		// produce is built directly (the result is the same, without marshalling and parsing).
		if data, ok := asJSONData(q.mapOnce(), 0); ok {
			q.parsedEvalCtxAsData = data

			return data, nil
		}
		raw, err := q.jsonOnce()
		if err != nil {
			return nil, err
		}
		var data any
		if unmarshalErr := json.Unmarshal(raw, &data); unmarshalErr != nil {
			return nil, unmarshalErr
		}
		q.parsedEvalCtxAsData = data
	}

	return q.parsedEvalCtxAsData, nil
}

// evaluateQuery evaluates the query of the rule. A nikunjy query that was validated with its
// rule has its evaluator found by the hash that validation saved in the rule, which does not
// read the query. Any other query is read through the compiled query cache.
func (r *Rule) evaluateQuery(queryCtx *queryContext) bool {
	if r.QueryHash != 0 { // the rule was validated
		raw := r.GetQuery()
		if ev := queryCtx.svc.nikunjy.lookup(r.QueryHash, raw); ev != nil {
			return evaluateNikunjy(ev, raw, queryCtx)
		}
	}

	return evaluateRule(queryCtx.svc.compiledQuery(r.GetQuery()), queryCtx)
}

// evaluateNikunjy is evaluateRule for the nikunjy query that ev evaluates.
//
//nolint:nonamedreturns // recover sets the result
func evaluateNikunjy(ev *parser.Evaluator, query string, queryCtx *queryContext) (success bool) {
	// Catch any panics that may occur during evaluation
	defer func() {
		if r := recover(); r != nil {
			queryCtx.svc.logger.ErrorContext(context.Background(), "panic recovered while evaluating query",
				slog.Any("panic", r),
				slog.String("query", query),
			)
			success = false
		}
	}()

	return processNikunjy(queryCtx.svc.logger, ev, query, queryCtx.mapOnce())
}

func processNikunjy(logger *slog.Logger, ev *parser.Evaluator, query string, mapCtx map[string]any) bool {
	ok, err := ev.Process(mapCtx)
	if err != nil {
		logger.Error("error while evaluating the nikunjy query",
			slog.String("query", query), slog.Any("error", err))

		return false
	}

	return ok
}

//nolint:nonamedreturns // recover sets the result
func evaluateRule(compiled *compiledQuery, queryCtx *queryContext) (success bool) {
	query := compiled.trimmed
	if query == "" {
		return true
	}
	mapCtx := queryCtx.mapOnce()

	// Catch any panics that may occur during evaluation
	defer func() {
		if r := recover(); r != nil {
			queryCtx.svc.logger.ErrorContext(context.Background(), "panic recovered while evaluating query",
				slog.Any("panic", r),
				slog.String("query", query),
			)
			success = false
		}
	}()

	// mapCtx is shared by every rule of the flag: a query engine must not modify it.
	switch compiled.format {
	case JSONLogicQueryFormat:
		data, err := queryCtx.dataOnce()
		if err != nil {
			queryCtx.svc.logger.ErrorContext(
				context.Background(),
				"error while marshalling the context for the jsonlogic query",
				slog.Any("mapCtx", mapCtx),
				slog.Any("error", err.Error()),
			)

			return false
		}
		result, err := jsonlogic.ApplyInterface(compiled.jsonLogic, data)
		if err != nil {
			queryCtx.svc.logger.ErrorContext(context.Background(), "error while evaluating the jsonlogic query",
				slog.String("query", query), slog.Any("error", err.Error()))

			return false
		}

		return isJSONLogicTruthy(result)
	default:
		ev, err := queryCtx.svc.nikunjy.get(query)
		if err != nil {
			queryCtx.svc.logger.Error("error while parsing the nikunjy query",
				slog.String("query", query), slog.Any("error", err))

			return false
		}

		return processNikunjy(queryCtx.svc.logger, ev, query, mapCtx)
	}
}

// EvaluateProgressiveRollout is evaluating the progressive rollout for the rule.
func (r *Rule) EvaluateProgressiveRollout(
	key, flagName string,
	evaluationDate time.Time,
) (string, error) {
	progressiveRolloutMaxPercentage := uint32(maxPercentage * PercentageMultiplier)
	hashID := utils.BuildHash(flagName, key, progressiveRolloutMaxPercentage)
	variation, err := r.getVariationFromProgressiveRollout(hashID, evaluationDate)
	if err != nil {
		return variation, err
	}

	return variation, nil
}

// EvaluatePercentageRollout is evaluating the percentage rollout for the rule.
//
// The hash modulus is the legacy one: the raw percentages are summed, then scaled and truncated. Changing it would
// move users who already have a variation. The sum used to be done in map iteration order, and float addition is not
// associative, so it was not deterministic for 3 variations or more; it is now done in the bucket order. A hash
// that is beyond the end of the last bucket (the modulus can be one above it because of float rounding) gets the
// last variation that has a share, instead of an error.
func (r *Rule) EvaluatePercentageRollout(key, flagName string) (string, error) {
	percentage := r.GetPercentages()

	// we need to sort the names to be sure we are constantly affecting the users to the same bucket.
	// Map are not ordered in GO, so we have to order the variationNames to be able to compute the same numbers for the
	// buckets every time we are in this function.
	var buf [16]string
	names := buf[:0]
	for k := range percentage {
		names = append(names, k)
	}
	slices.Sort(names)

	// HACK: we are iterating in reverse order to support the legacy format of the flags (before 1.0.0) and to be sure
	// to always have "True" before "False"
	sum := float64(0)
	for _, name := range slices.Backward(names) {
		sum += percentage[name]
	}
	hash := utils.BuildHash(flagName, key, uint32(sum*PercentageMultiplier))

	return variationFromBuckets(percentage, names, hash)
}

// IsDynamic is a function that allows to know if the rule has a dynamic result or not.
func (r *Rule) IsDynamic() bool {
	hasPercentage100 := false
	for _, percentage := range r.GetPercentages() {
		if percentage == maxPercentage {
			hasPercentage100 = true

			break
		}
	}

	return r.ProgressiveRollout != nil ||
		(r.Percentages != nil && len(r.GetPercentages()) > 0 && !hasPercentage100)
}

func (r *Rule) getVariationFromProgressiveRollout(
	hash uint32,
	evaluationDate time.Time,
) (string, error) {
	isRolloutValid := r.ProgressiveRollout != nil &&
		r.ProgressiveRollout.Initial != nil &&
		r.ProgressiveRollout.Initial.Date != nil &&
		r.ProgressiveRollout.Initial.Variation != nil &&
		r.ProgressiveRollout.End != nil &&
		r.ProgressiveRollout.End.Date != nil &&
		r.ProgressiveRollout.End.Variation != nil &&
		r.ProgressiveRollout.End.Date.After(*r.ProgressiveRollout.Initial.Date)

	if !isRolloutValid {
		return "", errErrorInTheProgressive
	}

	if evaluationDate.Before(*r.ProgressiveRollout.Initial.Date) {
		return *r.ProgressiveRollout.Initial.Variation, nil
	}

	// We are between initial and end
	initialPercentage := r.ProgressiveRollout.Initial.getPercentage() * PercentageMultiplier
	// The rule is shared by all the evaluations of the flag (concurrently): it must never be
	// written here, an end percentage outside of ]0, 100] means 100.
	endPercentage := r.ProgressiveRollout.End.getPercentage()
	if endPercentage == 0 || endPercentage > 100 {
		endPercentage = 100
	}
	endPercentage *= PercentageMultiplier
	nbSec := r.ProgressiveRollout.End.Date.Unix() - r.ProgressiveRollout.Initial.Date.Unix()
	percentage := endPercentage - initialPercentage
	percentPerSec := percentage / float64(nbSec)

	var currentPercentage float64
	// Cap the percentage at endPercentage if evaluation date is at or after end date
	if !evaluationDate.Before(*r.ProgressiveRollout.End.Date) {
		currentPercentage = endPercentage
	} else {
		c := evaluationDate.Unix() - r.ProgressiveRollout.Initial.Date.Unix()
		currentPercentage = float64(c)*percentPerSec + initialPercentage
	}

	// The percentage represents how many users get the END variation
	// Users with hash < currentPercentage get END variation
	// Users with hash >= currentPercentage get INITIAL variation
	if hash < uint32(currentPercentage) {
		return r.ProgressiveRollout.End.getVariation(), nil
	}

	return r.ProgressiveRollout.Initial.getVariation(), nil
}

func variationFromBuckets(percentage map[string]float64, names []string, hash uint32) (string, error) {
	start := float64(0)
	last := ""
	for _, name := range slices.Backward(names) {
		p := percentage[name]
		end := start + (p * PercentageMultiplier)
		if uint32(start) <= hash && uint32(end) > hash {
			return name, nil
		}
		if p > 0 {
			last = name
		}
		start = end
	}
	if last != "" {
		// the hash is beyond the end of the last bucket: rounding of the modulus, the last bucket takes it.
		return last, nil
	}

	return "", errImpossibleToFindThe
}

// MergeRules is merging 2 rules.
// It is used when we have to update a rule in a scheduled rollout.
func (r *Rule) MergeRules(updatedRule Rule) {
	if updatedRule.Disable != nil {
		r.Disable = updatedRule.Disable
	}

	if updatedRule.Query != nil {
		r.Query = updatedRule.Query
		r.QueryHash = 0
	}

	if updatedRule.VariationResult != nil {
		r.VariationResult = updatedRule.VariationResult
	}

	if updatedRule.ProgressiveRollout != nil {
		c := r.GetProgressiveRollout()
		if updatedRule.ProgressiveRollout.Initial != nil {
			c.Initial.mergeStep(updatedRule.ProgressiveRollout.Initial)
		}

		if updatedRule.ProgressiveRollout.End != nil {
			c.End.mergeStep(updatedRule.ProgressiveRollout.End)
		}
		r.ProgressiveRollout = &c
	}

	if updatedRule.Percentages != nil {
		updatedPercentages := updatedRule.GetPercentages()
		mergedPercentages := r.GetPercentages()
		for key, percentage := range updatedPercentages {
			// When you set a negative percentage we are not taking it in consideration.
			if percentage < 0 {
				delete(mergedPercentages, key)

				continue
			}
			mergedPercentages[key] = percentage
		}
		r.Percentages = &mergedPercentages
	}
}

// GetTrimmedQuery is removing the break lines and return.
func (r *Rule) GetTrimmedQuery() string {
	return utils.StrTrim(r.GetQuery())
}

// GetQueryFormat is returning the format used for the query.
func (r *Rule) GetQueryFormat() QueryFormat {
	return queryFormatOf(r.GetTrimmedQuery())
}

// queryFormatOf returns the format of an already trimmed query.
func queryFormatOf(trimmedQuery string) QueryFormat {
	if utils.IsJSONObject(trimmedQuery) {
		return JSONLogicQueryFormat
	}

	return NikunjyQueryFormat
}

func (r *Rule) GetQuery() string {
	if r.Query == nil {
		return ""
	}

	return *r.Query
}

func (r *Rule) GetVariationResult() string {
	if r.VariationResult == nil {
		return ""
	}

	return *r.VariationResult
}

func (r *Rule) GetName() string {
	if r.Name == nil {
		return ""
	}

	return *r.Name
}

func (r *Rule) GetPercentages() map[string]float64 {
	if r.Percentages == nil {
		return map[string]float64{}
	}

	return *r.Percentages
}

func (r *Rule) IsDisable() bool {
	if r.Disable == nil {
		return false
	}

	return *r.Disable
}

func (r *Rule) GetProgressiveRollout() ProgressiveRollout {
	if r.ProgressiveRollout == nil {
		return ProgressiveRollout{
			Initial: &ProgressiveRolloutStep{},
			End:     &ProgressiveRolloutStep{},
		}
	}

	return *r.ProgressiveRollout
}

// MergeSetOfRules is taking a collection of rules and merge it with the updates
// from a schedule steps.
// If you want to edit a rule this rule should have a name already to be able to
// target the updates to the right place.
func MergeSetOfRules(initialRules, updates []Rule) *[]Rule {
	collection := initialRules
	modified := make(map[string]Rule, 0)
	for _, update := range updates {
		ruleName := update.Name
		if ruleName != nil {
			modified[update.GetName()] = update
		}
	}

	mergedUpdates := make([]string, 0)
	for index, rule := range collection {
		if _, ok := modified[rule.GetName()]; ok {
			rule.MergeRules(modified[rule.GetName()])
			collection[index] = rule
			mergedUpdates = append(mergedUpdates, rule.GetName())
		}
	}

	for _, update := range updates {
		if !utils.Contains(mergedUpdates, update.GetName()) {
			// The rule is edited by the following updates (see MergeRules): it must not share its
			// percentages and progressive rollout with the update, which is read by every evaluation.
			collection = append(collection, cloneRuleForMerge(update))
		}
	}

	return &collection
}

// cloneRuleForMerge copies what MergeRules edits in place (the percentages and the steps of the
// progressive rollout), the other fields are replaced and never edited.
func cloneRuleForMerge(r Rule) Rule {
	if r.Percentages != nil {
		p := maps.Clone(*r.Percentages)
		r.Percentages = &p
	}
	if r.ProgressiveRollout != nil {
		pr := *r.ProgressiveRollout
		if pr.Initial != nil {
			initial := *pr.Initial
			pr.Initial = &initial
		}
		if pr.End != nil {
			end := *pr.End
			pr.End = &end
		}
		r.ProgressiveRollout = &pr
	}

	return r
}
