package controlplane

import (
	"math"
	"slices"
	"strconv"
)

// DefaultMaxObjects is the default bound on the number of objects of a manifest (validation and memory). The
// relay makes it a setting; pass WithMaxObjects to change it.
const DefaultMaxObjects = 1000

const (
	maxNameLen = 63
	maxPercent = 100
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

// ValidateManifest runs the checks the relay runs on a manifest (an empty header means the current one) and
// returns a *ValidationError listing every problem, an ErrUnsupportedAPIVersion or an ErrWrongKind.
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
		probs.addf("channel", "is required")
	}

	if manifest.Generation < 1 {
		probs.addf("generation", "must be at least 1")
	}

	if len(manifest.Objects) > cfg.maxObjects {
		probs.addf("objects", "has %d entries, the limit is %d", len(manifest.Objects), cfg.maxObjects)
	}

	seen := make(map[string]struct{}, len(manifest.Objects))

	for i := range manifest.Objects {
		path := "objects[" + strconv.Itoa(i) + "]"
		obj := &manifest.Objects[i]

		if _, dup := seen[obj.Name]; dup {
			probs.addf(path+".name", "duplicate name %q", obj.Name)
		}

		seen[obj.Name] = struct{}{}

		validateObject(&probs, path, obj)
	}

	return probs.err()
}

func validateObject(probs *problems, path string, obj *ManifestObject) {
	if !validName(obj.Name) {
		probs.addf(path+".name", "%q does not match ^[a-z][a-z0-9-]{0,62}$", obj.Name)
	}

	validateKindFormat(probs, path, obj)

	if obj.Connection == "" {
		probs.addf(path+".connection", "is required")
	}

	if obj.Key == "" {
		probs.addf(path+".key", "is required")
	}

	if obj.Version != 0 && obj.Version != ObjectVersion1 {
		probs.addf(path+".version", "%d is not supported", obj.Version)
	}

	if obj.Size < 1 {
		probs.addf(path+".size", "must be at least 1")
	}

	validateChecksum(probs, path+".checksum", obj.Checksum)
}

func validateKindFormat(probs *problems, path string, obj *ManifestObject) {
	switch obj.Kind {
	case KindFlags:
		if obj.Format != FormatYAML && obj.Format != FormatJSON && obj.Format != FormatTOML {
			probs.addf(path+".format", "%q is not yaml, json or toml", obj.Format)
		}
	case KindFlagSets:
		if obj.Format != FormatYAML && obj.Format != FormatJSON {
			probs.addf(path+".format", "%q is not yaml or json", obj.Format)
		}
	default:
		probs.addf(path+".kind", "%q is not flags or flagSets", obj.Kind)
	}
}

func validateChecksum(probs *problems, path string, sum Checksum) {
	if sum.Algorithm != AlgorithmSHA256 {
		probs.addf(path+".algorithm", "%q is not sha256", sum.Algorithm)

		return
	}

	if !isLowerHex(sum.Value, sha256HexLen) {
		probs.addf(path+".value", "must be %d lower-case hex characters", sha256HexLen)
	}
}

// ValidateFlagSet checks a flag-set document. When manifest is not nil every source must name an object of kind
// flags of the manifest. Names and API keys must be unique; the error never contains a key value.
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
		path := "flagSets[" + strconv.Itoa(i) + "]"
		set := &doc.FlagSets[i]

		if !validName(set.Name) {
			probs.addf(path+".name", "%q does not match ^[a-z][a-z0-9-]{0,62}$", set.Name)
		}

		if _, dup := names[set.Name]; dup {
			probs.addf(path+".name", "duplicate name %q", set.Name)
		}

		names[set.Name] = struct{}{}

		validateSources(&probs, path, set, manifest != nil, flagObjects)
		validateAPIKeys(&probs, path, set, keys)
	}

	return probs.err()
}

func validateSources(probs *problems, path string, set *FlagSet, resolve bool, flagObjects map[string]struct{}) {
	if len(set.Sources) == 0 {
		probs.addf(path+".sources", "needs at least one source")
	}

	for i, source := range set.Sources {
		if _, ok := flagObjects[source]; resolve && !ok {
			probs.addf(
				path+".sources["+strconv.Itoa(i)+"]",
				"%q is not an object of kind flags in the manifest",
				source,
			)
		}
	}
}

// validateAPIKeys checks the keys of one flag set; keys maps a key to the path of its first use.
func validateAPIKeys(probs *problems, path string, set *FlagSet, keys map[string]string) {
	for i, key := range set.APIKeys {
		keyPath := path + ".apiKeys[" + strconv.Itoa(i) + "]"

		if key == "" {
			probs.addf(keyPath, "is empty")

			continue
		}

		if first, dup := keys[key]; dup {
			probs.addf(keyPath, "duplicate of %s", first)

			continue
		}

		keys[key] = keyPath
	}
}

// ValidateFlags checks the structure of a flag file: names, that rules point at declared variations,
// percentages in [0, 100] and complete progressive rollouts. It does not compile queries; the engine does.
func ValidateFlags(flags Flags) error {
	var probs problems

	names := make([]string, 0, len(flags))
	for name := range flags {
		names = append(names, name)
	}

	slices.Sort(names)

	for _, name := range names {
		flag := flags[name]

		if name == "" {
			probs.addf("", "flag name is empty")
		}

		if flag.Converter == "" && len(flag.Variations) == 0 {
			probs.addf(name+".variations", "is required")
		}

		if flag.Converter == "" && flag.DefaultRule == nil {
			probs.addf(name+".defaultRule", "is required")
		}

		validateRule(&probs, name+".defaultRule", flag.DefaultRule, flag.Variations)

		for i := range flag.Targeting {
			validateRule(&probs, name+".targeting["+strconv.Itoa(i)+"]", &flag.Targeting[i], flag.Variations)
		}
	}

	return probs.err()
}

func validateRule(probs *problems, path string, rule *Rule, variations map[string]any) {
	if rule == nil {
		return
	}

	if rule.Variation != "" {
		checkVariation(probs, path+".variation", rule.Variation, variations)
	}

	percentages := make([]string, 0, len(rule.Percentage))
	for name := range rule.Percentage {
		percentages = append(percentages, name)
	}

	slices.Sort(percentages)

	for _, name := range percentages {
		checkVariation(probs, path+".percentage."+name, name, variations)

		if value := rule.Percentage[name]; value < 0 || value > maxPercent || math.IsNaN(value) {
			probs.addf(path+".percentage."+name, "%v is not between 0 and 100", value)
		}
	}

	if rule.Variation == "" && len(rule.Percentage) == 0 && rule.ProgressiveRollout == nil {
		probs.addf(path, "needs a variation, a percentage or a progressiveRollout")
	}

	validateProgressive(probs, path+".progressiveRollout", rule.ProgressiveRollout, variations)
}

func validateProgressive(probs *problems, path string, rollout *ProgressiveRollout, variations map[string]any) {
	if rollout == nil {
		return
	}

	if rollout.Initial == nil || rollout.End == nil {
		probs.addf(path, "needs initial and end")

		return
	}

	if rollout.Initial.Variation != "" {
		checkVariation(probs, path+".initial.variation", rollout.Initial.Variation, variations)
	}

	if rollout.End.Variation != "" {
		checkVariation(probs, path+".end.variation", rollout.End.Variation, variations)
	}
}

// checkVariation reports a variation name that the flag does not declare.
func checkVariation(probs *problems, path, name string, variations map[string]any) {
	if _, ok := variations[name]; !ok && len(variations) > 0 {
		probs.addf(path, "variation %q is not declared", name)
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
