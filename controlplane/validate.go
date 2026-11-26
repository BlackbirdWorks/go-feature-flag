package controlplane

import (
	"math"
	"slices"
	"strconv"
)

// DefaultMaxObjects is the default bound on the number of objects of a manifest (validation and memory). The
// relay makes it a setting with the same default; pass WithMaxObjects to change it.
const DefaultMaxObjects = 20000

const (
	maxNameLen = 63
	// percentageMultiplier is the engine's scale of a percentage; the scaled sum must fit a uint32.
	percentageMultiplier = 1000
)

// ValidateOption changes how a Validate function checks.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	maxObjects int
}

// WithMaxObjects sets the largest number of objects a manifest may list (default DefaultMaxObjects). A value
// of zero or less keeps the default.
func WithMaxObjects(limit int) ValidateOption {
	return func(cfg *validateConfig) {
		if limit > 0 {
			cfg.maxObjects = limit
		}
	}
}

// docEntry is the entry of a problem of the document as a whole.
func docEntry() entry {
	return entry{kind: EntryDocument, name: "", index: NoIndex}
}

// ValidateManifest runs the checks the relay runs on a manifest (an empty header means the current one).
//
// A wrong kind or an unsupported apiVersion rejects the document whole (*KindError, *UnsupportedVersionError,
// both match ErrInvalid too). Every other problem is a *ValidationError whose FieldErrors name the entry, so a
// caller can keep the good entries: an object with a problem is one that has a FieldError of Kind EntryObject
// with its Index. Of two objects with one name the first keeps it and the later one has the problem. An
// object with a newer content version is an entry problem (CodeUnsupportedVersion), not a failure of the
// manifest.
func ValidateManifest(manifest *Manifest, opts ...ValidateOption) error {
	if manifest == nil {
		return ErrNilDocument
	}

	cfg := validateConfig{maxObjects: DefaultMaxObjects}
	for _, opt := range opts {
		opt(&cfg)
	}

	err := checkHeader(orDefault(manifest.APIVersion, APIVersion), orDefault(manifest.Kind, DocumentKindManifest),
		DocumentKindManifest)
	if err != nil {
		return err
	}

	var probs problems

	if manifest.Channel == "" {
		probs.addf(docEntry(), CodeMissingValue, "channel", "is required")
	}

	if manifest.Generation < 1 {
		probs.addf(docEntry(), CodeInvalidValue, "generation", "must be at least 1")
	}

	if len(manifest.Objects) > cfg.maxObjects {
		probs.addf(docEntry(), CodeTooManyObjects, "objects", "has %d entries, the limit is %d",
			len(manifest.Objects), cfg.maxObjects)
	}

	seen := make(map[string]struct{}, len(manifest.Objects))

	for i := range manifest.Objects {
		obj := &manifest.Objects[i]
		owner := entry{kind: EntryObject, name: obj.Name, index: i}
		path := "objects[" + strconv.Itoa(i) + "]"

		if _, dup := seen[obj.Name]; dup {
			probs.addf(owner, CodeDuplicateName, path+".name", "duplicate name %q", obj.Name)
		}

		seen[obj.Name] = struct{}{}

		validateObject(&probs, owner, path, obj)
	}

	return probs.err()
}

func validateObject(probs *problems, owner entry, path string, obj *ManifestObject) {
	if !validName(obj.Name) {
		probs.addf(owner, CodeInvalidName, path+".name", "%q does not match ^[a-z][a-z0-9-]{0,62}$", obj.Name)
	}

	validateKindFormat(probs, owner, path, obj)

	if obj.Connection == "" {
		probs.addf(owner, CodeMissingValue, path+".connection", "is required")
	}

	if obj.Key == "" {
		probs.addf(owner, CodeMissingValue, path+".key", "is required")
	}

	switch {
	case obj.Version < 0:
		probs.addf(owner, CodeInvalidValue, path+".version", "must not be negative")
	case obj.Version > ObjectVersion1:
		probs.addf(owner, CodeUnsupportedVersion, path+".version", "%d is not supported", obj.Version)
	}

	if obj.Size < 0 {
		probs.addf(owner, CodeInvalidValue, path+".size", "must not be negative")
	}

	err := validChecksum(obj.Checksum)
	if err != nil {
		probs.addf(owner, CodeInvalidChecksum, path+".checksum", "%s", err.Error())
	}
}

func validateKindFormat(probs *problems, owner entry, path string, obj *ManifestObject) {
	switch obj.Kind {
	case KindFlags:
		if obj.Format != FormatYAML && obj.Format != FormatJSON && obj.Format != FormatTOML {
			probs.addf(owner, CodeInvalidValue, path+".format", "%q is not yaml, json or toml", obj.Format)
		}
	case KindFlagSets:
		if obj.Format != FormatYAML && obj.Format != FormatJSON {
			probs.addf(owner, CodeInvalidValue, path+".format", "%q is not yaml or json", obj.Format)
		}
	default:
		probs.addf(owner, CodeInvalidValue, path+".kind", "%q is not flags or flagSets", obj.Kind)
	}
}

// ValidateFlagSet checks a flag-set document. When manifest is not nil every source must name an object of kind
// flags of the manifest. Header failures are returned as for ValidateManifest.
//
// Problems name the flag set (Kind EntryFlagSet, Name, Index) so a caller can keep the good flag sets. Of two
// flag sets with one name, and of an API key held by two flag sets, the first in document order keeps it and
// the later flag set has the problem (CodeDuplicateName, CodeDuplicateKey). The text of an API key is never
// printed.
func ValidateFlagSet(doc *FlagSetDocument, manifest *Manifest) error {
	if doc == nil {
		return ErrNilDocument
	}

	err := checkHeader(orDefault(doc.APIVersion, APIVersion), orDefault(doc.Kind, DocumentKindFlagSet),
		DocumentKindFlagSet)
	if err != nil {
		return err
	}

	flagObjects := map[string]struct{}{}

	if manifest != nil {
		for i := range manifest.Objects {
			if manifest.Objects[i].Kind == KindFlags {
				flagObjects[manifest.Objects[i].Name] = struct{}{}
			}
		}
	}

	var probs problems

	names := make(map[string]struct{}, len(doc.FlagSets))
	keys := map[string]string{}

	for i := range doc.FlagSets {
		set := &doc.FlagSets[i]
		owner := entry{kind: EntryFlagSet, name: set.Name, index: i}
		path := "flagSets[" + strconv.Itoa(i) + "]"

		if !validName(set.Name) {
			probs.addf(owner, CodeInvalidName, path+".name", "%q does not match ^[a-z][a-z0-9-]{0,62}$", set.Name)
		}

		if _, dup := names[set.Name]; dup {
			probs.addf(owner, CodeDuplicateName, path+".name", "duplicate name %q", set.Name)
		}

		names[set.Name] = struct{}{}

		validateSources(&probs, owner, path, set, manifest != nil, flagObjects)
		validateAPIKeys(&probs, owner, path, set, keys)
	}

	return probs.err()
}

func validateSources(
	probs *problems,
	owner entry,
	path string,
	set *FlagSet,
	resolve bool,
	flagObjects map[string]struct{},
) {
	if len(set.Sources) == 0 {
		probs.addf(owner, CodeMissingValue, path+".sources", "needs at least one source")
	}

	for i, source := range set.Sources {
		if _, ok := flagObjects[source]; resolve && !ok {
			probs.addf(owner, CodeUnresolvedSource, path+".sources["+strconv.Itoa(i)+"]",
				"%q is not an object of kind flags in the manifest", source)
		}
	}
}

// validateAPIKeys checks the keys of one flag set; keys maps a key to the path of its first use.
func validateAPIKeys(probs *problems, owner entry, path string, set *FlagSet, keys map[string]string) {
	for i, key := range set.APIKeys {
		keyPath := path + ".apiKeys[" + strconv.Itoa(i) + "]"

		if key == "" {
			probs.addf(owner, CodeInvalidValue, keyPath, "is empty")

			continue
		}

		if first, dup := keys[key]; dup {
			probs.addf(owner, CodeDuplicateKey, keyPath, "duplicate of %s", first)

			continue
		}

		keys[key] = keyPath
	}
}

// ValidateFlags checks a flag file with the structural rules of the engine's own validation, so that a flag
// accepted here is not dropped when the relay loads it: variations present, not null and all of one type, a
// default rule, enabled targeting rules with a query and an outcome, declared variations, valid percentages
// (finite, not negative, a non-zero sum), complete progressive rollouts (both steps, variations, dates in
// order), and the same checks on scheduled steps (whose rules may be partial updates of a named rule). A
// disabled targeting rule is not checked. It does not compile queries; the engine does.
//
// Problems have Kind EntryFlag and the flag name in Name, so a caller can keep the good flags.
func ValidateFlags(flags Flags) error {
	var probs problems

	names := make([]string, 0, len(flags))
	for name := range flags {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		check := flagCheck{probs: &probs, owner: entry{kind: EntryFlag, name: name, index: NoIndex}, variations: nil}

		if name == "" {
			probs.addf(check.owner, CodeInvalidName, "", "flag name is empty")
		}

		flag := flags[name]
		check.flag(name, &flag)
	}

	return probs.err()
}

// flagCheck validates one flag; variations are the ones rules are checked against.
type flagCheck struct {
	probs      *problems
	variations map[string]any
	owner      entry
}

func (c *flagCheck) flag(name string, flag *Flag) {
	c.variations = flag.Variations

	if len(flag.Variations) == 0 {
		c.probs.addf(c.owner, CodeMissingValue, name+".variations", "is required")
	}

	c.variationTypes(name+".variations", flag.Variations)

	if flag.DefaultRule == nil {
		c.probs.addf(c.owner, CodeMissingValue, name+".defaultRule", "is required")
	}

	c.rule(name+".defaultRule", flag.DefaultRule, true, false)

	for i := range flag.Targeting {
		c.rule(name+".targeting["+strconv.Itoa(i)+"]", &flag.Targeting[i], false, false)
	}

	for i := range flag.ScheduledRollout {
		c.step(name+".scheduledRollout["+strconv.Itoa(i)+"]", flag.Variations, &flag.ScheduledRollout[i])
	}
}

// step checks a scheduled step: its rules may update a named rule, so they are checked as partial.
func (c *flagCheck) step(path string, flagVariations map[string]any, step *ScheduledStep) {
	if step.Date == nil {
		c.probs.addf(c.owner, CodeMissingValue, path+".date", "is required: a step without a date never applies")
	}

	c.variations = flagVariations

	if len(step.Variations) > 0 {
		c.variations = step.Variations
		c.variationTypes(path+".variations", step.Variations)
	}

	c.rule(path+".defaultRule", step.DefaultRule, true, true)

	for i := range step.Targeting {
		c.rule(path+".targeting["+strconv.Itoa(i)+"]", &step.Targeting[i], false, true)
	}
}

// variationTypes checks that no variation is null and that all have the same JSON type.
func (c *flagCheck) variationTypes(path string, variations map[string]any) {
	names := make([]string, 0, len(variations))
	for name := range variations {
		names = append(names, name)
	}

	slices.Sort(names)

	expected := ""

	for _, name := range names {
		kind := variationType(variations[name])
		if kind == "" {
			c.probs.addf(c.owner, CodeInvalidValue, path+"."+name, "must have a value of a supported type, not null")

			continue
		}

		if expected == "" {
			expected = kind

			continue
		}

		if kind != expected {
			c.probs.addf(c.owner, CodeInvalidValue, path+"."+name, "all variations must have the same type")
		}
	}
}

func variationType(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return "number"
	case []any:
		return "list"
	case map[string]any:
		return "object"
	default:
		return ""
	}
}

func (c *flagCheck) declared(path, name string) {
	if _, ok := c.variations[name]; !ok {
		c.probs.addf(c.owner, CodeUndeclaredVariation, path, "variation %q is not declared", name)
	}
}

// rule checks a rule. partial is set for the rules of a scheduled step: they may leave out what the rule they
// update already has.
func (c *flagCheck) rule(path string, rule *Rule, isDefault, partial bool) {
	if rule == nil || !isDefault && rule.Disable != nil && *rule.Disable {
		return
	}

	if !partial {
		c.outcome(path, rule, isDefault)
	}

	c.percentages(path, rule)
	c.progressive(path+".progressiveRollout", rule.ProgressiveRollout, partial)

	if rule.Percentage == nil && rule.ProgressiveRollout == nil && rule.Variation != nil {
		c.declared(path+".variation", *rule.Variation)
	}
}

// outcome checks that an enabled targeting rule has a query and that every rule has a way to pick a variation.
func (c *flagCheck) outcome(path string, rule *Rule, isDefault bool) {
	if !isDefault && rule.Query == nil {
		c.probs.addf(c.owner, CodeMissingValue, path+".query", "is required for a targeting rule")
	}

	if rule.Percentage == nil && rule.ProgressiveRollout == nil && rule.Variation == nil {
		c.probs.addf(c.owner, CodeMissingValue, path, "needs a variation, a percentage or a progressiveRollout")
	}
}

func (c *flagCheck) percentages(path string, rule *Rule) {
	if rule.Percentage == nil {
		return
	}

	if len(rule.Percentage) == 0 {
		c.probs.addf(c.owner, CodeInvalidValue, path+".percentage", "must not be empty")

		return
	}

	names := make([]string, 0, len(rule.Percentage))
	for name := range rule.Percentage {
		names = append(names, name)
	}

	slices.Sort(names)

	total := 0.0

	for _, name := range names {
		value := rule.Percentage[name]
		field := path + ".percentage." + name

		c.declared(field, name)

		if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
			c.probs.addf(c.owner, CodeInvalidValue, field, "must be a finite number, not negative")

			continue
		}

		total += value
	}

	switch {
	case total == 0:
		c.probs.addf(c.owner, CodeInvalidValue, path+".percentage", "the sum of the percentages must not be zero")
	case total*percentageMultiplier > math.MaxUint32:
		c.probs.addf(c.owner, CodeInvalidValue, path+".percentage", "the sum of the percentages is too large")
	}
}

func (c *flagCheck) progressive(path string, rollout *ProgressiveRollout, partial bool) {
	if rollout == nil {
		return
	}

	if rollout.Initial == nil || rollout.End == nil {
		c.probs.addf(c.owner, CodeMissingValue, path, "needs initial and end")

		return
	}

	c.rolloutStep(path+".initial", rollout.Initial, partial)
	c.rolloutStep(path+".end", rollout.End, partial)

	initial, end := rollout.Initial, rollout.End
	if initial.Date != nil && end.Date != nil && !end.Date.After(*initial.Date) {
		c.probs.addf(c.owner, CodeInvalidValue, path+".end.date", "must be after the initial date")
	}

	if initial.Percentage != nil && end.Percentage != nil && *end.Percentage < *initial.Percentage {
		c.probs.addf(c.owner, CodeInvalidValue, path+".end.percentage", "must not be lower than the initial percentage")
	}
}

// rolloutStep checks one end of a progressive rollout.
func (c *flagCheck) rolloutStep(path string, step *ProgressiveRolloutStep, partial bool) {
	switch {
	case step.Variation != nil:
		c.declared(path+".variation", *step.Variation)
	case !partial:
		c.probs.addf(c.owner, CodeMissingValue, path+".variation", "is required")
	}

	if step.Date == nil && !partial {
		c.probs.addf(c.owner, CodeMissingValue, path+".date", "is required")
	}

	if value := step.Percentage; value != nil && (*value < 0 || math.IsNaN(*value) || math.IsInf(*value, 0)) {
		c.probs.addf(c.owner, CodeInvalidValue, path+".percentage", "must be a finite number, not negative")
	}
}

// validName reports whether text matches ^[a-z][a-z0-9-]{0,62}$.
func validName(text string) bool {
	if text == "" || len(text) > maxNameLen {
		return false
	}

	for i := range len(text) {
		char := text[i]
		if char >= 'a' && char <= 'z' || i > 0 && (char >= '0' && char <= '9' || char == '-') {
			continue
		}

		return false
	}

	return true
}

func isLowerHex(text string, length int) bool {
	if len(text) != length {
		return false
	}

	for i := range len(text) {
		char := text[i]
		if char >= '0' && char <= '9' || char >= 'a' && char <= 'f' {
			continue
		}

		return false
	}

	return true
}
