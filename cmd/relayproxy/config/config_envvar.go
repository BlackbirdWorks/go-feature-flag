package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	maps0 "maps"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/knadh/koanf/maps"

	"github.com/blackbirdworks/semaphore/cmdhelpers/cmdlog"
)

const (
	// minArrayEnvPaths is the number of parts of an array environment variable: <ARRAY>_<INDEX>_<FIELD>.
	minArrayEnvPaths = 3
)

var (
	errEnvProviderDoesNot  = errors.New("env provider does not support this method")
	errEnvironmentVariable = errors.New("environment variable")
	errTheConfigEntryAt    = errors.New("the config entry at index")
)

// environProvider is koanf's env provider reading the variables from environ instead of [os.Environ], so
// the tests do not have to change the environment of the process.
type environProvider struct {
	environ func() []string
	cb      func(key, value string) (string, any)
	// skip (optional) tells the variables to leave out, by the name without the prefix and the value.
	skip func(key, value string) bool
	// skipped (optional) receives the names of the variables left out.
	skipped func(names []string)
	prefix  string
}

// ReadBytes is not supported by the environment provider.
func (*environProvider) ReadBytes() ([]byte, error) {
	return nil, errEnvProviderDoesNot
}

// Read returns the variables that start with the prefix, mapped with the callback, as a nested map.
func (p *environProvider) Read() (map[string]any, error) {
	envMap := make(map[string]any)

	var skipped []string

	for _, kv := range p.environ() {
		name, value, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(name, p.prefix) {
			continue
		}

		if p.skip != nil && p.skip(strings.TrimPrefix(name, p.prefix), value) {
			skipped = append(skipped, name)

			continue
		}

		key, mapped := p.cb(name, value)
		if key == "" {
			continue
		}

		envMap[key] = mapped
	}

	if p.skipped != nil && len(skipped) > 0 {
		slices.Sort(skipped)
		p.skipped(skipped)
	}

	return maps.Unflatten(envMap, "."), nil
}

var (
	// serviceLinkPortRegexp matches PORT_<port>_<PROTO> and its _ADDR, _PORT and _PROTO variants.
	serviceLinkPortRegexp = regexp.MustCompile(`^PORT_\d+_(TCP|UDP|SCTP)(_ADDR|_PORT|_PROTO)?$`)
	// serviceLinkNamedPortRegexp matches SERVICE_PORT_<NAME>.
	serviceLinkNamedPortRegexp = regexp.MustCompile(`^SERVICE_PORT_[A-Z0-9_]+$`)
)

// isServiceLink tells if the variable, given without its prefix, is one of the variables Kubernetes injects
// for the Services of the namespace (service links): <SERVICE>_SERVICE_HOST, <SERVICE>_SERVICE_PORT,
// <SERVICE>_SERVICE_PORT_<NAME>, <SERVICE>_PORT and <SERVICE>_PORT_<port>_<PROTO>[_ADDR|_PORT|_PROTO].
//
// The prefix of the settings is the name of the product, so a Service with that name makes Kubernetes
// inject variables under it. A variable is a service link only if both its name and its value have the form
// Kubernetes gives them, so a setting with a similar name and another value is still read.
func isServiceLink(name, value string) bool {
	switch {
	case name == "SERVICE_HOST":
		_, err := netip.ParseAddr(value)

		return err == nil
	case name == "SERVICE_PORT", serviceLinkNamedPortRegexp.MatchString(name):
		return isPortNumber(value)
	case name == "PORT":
		return isServiceLinkURL(value)
	case serviceLinkPortRegexp.MatchString(name):
		switch {
		case strings.HasSuffix(name, "_ADDR"):
			_, err := netip.ParseAddr(value)

			return err == nil
		case strings.HasSuffix(name, "_PORT"):
			return isPortNumber(value)
		case strings.HasSuffix(name, "_PROTO"):
			return slices.Contains([]string{"tcp", "udp", "sctp"}, value)
		default:
			return isServiceLinkURL(value)
		}
	}

	return false
}

func isPortNumber(value string) bool {
	port, err := strconv.Atoi(value)

	return err == nil && port > 0 && port < 65536
}

func isServiceLinkURL(value string) bool {
	for _, scheme := range []string{"tcp://", "udp://", "sctp://"} {
		if strings.HasPrefix(value, scheme) {
			return true
		}
	}

	return false
}

func (c *Loader) mapEnvVariablesProvider(prefix string, log *slog.Logger) *environProvider {
	return &environProvider{prefix: prefix, environ: c.environ(),
		skip: func(key, value string) bool { return prefix != "" && isServiceLink(key, value) },
		skipped: func(names []string) {
			// only the names are logged, the values may be anything.
			log.LogAttrs(
				context.Background(),
				slog.LevelDebug,
				"config: ignoring Kubernetes service link variables",
				slog.Any("names", names),
			)
		},
		cb: func(key, val string) (string, any) {
			key = strings.TrimPrefix(key, prefix)
			switch {
			case strings.HasPrefix(key, "RETRIEVERS"),
				strings.HasPrefix(key, "NOTIFIER"),
				strings.HasPrefix(key, "NOTIFIERS"),
				strings.HasPrefix(key, "FLAGSETS"),
				strings.HasPrefix(key, "EXPORTERS"):
				configMap := c.k.Raw()

				modifiedConfigMap, err := loadArrayEnv(key, val, configMap)
				if err != nil {
					log.LogAttrs(context.Background(), slog.LevelError,
						"config: error loading array env",
						slog.String("key", key),
						// the value of the variable may be a secret (API key, token, ...): only log its size.
						slog.Int("valueLength", len(val)),
						cmdlog.Err(err),
					)

					return key, val
				}
				// Update the global config with the modified configMap
				for configKey, configValue := range modifiedConfigMap {
					_ = c.k.Set(configKey, configValue)
				}

				return key, val
			case strings.HasSuffix(key, "KAFKA_ADDRESSES"),
				strings.HasSuffix(key, "APIKEYS"),
				strings.HasPrefix(key, "AUTHORIZEDKEYS_EVALUATION"),
				strings.HasPrefix(key, "AUTHORIZEDKEYS_ADMIN"):
				transformedKey := strings.ReplaceAll(strings.ToLower(key), "_", ".")

				return transformedKey, strings.Split(val, ",")
			case key == "OTEL_RESOURCE_ATTRIBUTES":
				c.parseOtelResourceAttributes(val, log)

				return key, val
			default:
				return strings.ReplaceAll(strings.ToLower(key), "_", "."), val
			}
		}}
}

// environ returns the source of the environment variables, [os.Environ] unless the loader was given another one.
func (c *Loader) environ() func() []string {
	if c.environFunc != nil {
		return c.environFunc
	}

	return os.Environ
}

// Load the ENV Like:RETRIEVERS_0_HEADERS_AUTHORIZATION.
func loadArrayEnv(envKey, val string, configMap map[string]any) (map[string]any, error) {
	paths := normalizePaths(envKey)
	if len(paths) < minArrayEnvPaths {
		return configMap, fmt.Errorf("%w %q: expected <ARRAY>_<INDEX>_<FIELD>", errEnvironmentVariable, envKey)
	}

	prefixKey := paths[0]

	configArray, ok := configMap[prefixKey].([]any)
	if !ok {
		// Initialize the array if it doesn't exist
		configArray = []any{}
		configMap[prefixKey] = configArray
	}

	index, err := strconv.Atoi(paths[1])
	if err != nil {
		return configMap, fmt.Errorf("environment variable %q: invalid index %q: %w", envKey, paths[1], err)
	}

	if index < 0 {
		return configMap, fmt.Errorf("%w %q: negative index %d", errEnvironmentVariable, envKey, index)
	}

	configItem, err := fetchOrInitConfigItemAtIndex(configArray, index)
	if err != nil {
		return configMap, fmt.Errorf("environment variable %q: %w", envKey, err)
	}

	keys := paths[2:]

	if shouldHandleRecursively(prefixKey, keys) {
		return handleRecursiveConfig(keys, val, configItem, configArray, index, configMap, prefixKey)
	}

	return handleDirectConfig(keys, val, configItem, configArray, index, configMap, prefixKey)
}

// normalizePaths splits the input string and converts all parts to lowercase.
func normalizePaths(s string) []string {
	paths := strings.Split(s, "_")
	for i, str := range paths {
		paths[i] = strings.ToLower(str)
	}

	return paths
}

// fetchOrInitConfigItemAtIndex retrieves or creates a config item at the specified index.
// An entry that is not a map (a scalar or a nested array from the config file) cannot take a
// field override, so it is an error rather than a silent overwrite; a null entry is replaced by a new map.
func fetchOrInitConfigItemAtIndex(configArray []any, index int) (map[string]any, error) {
	outRange := index >= len(configArray)
	if outRange || configArray[index] == nil {
		return make(map[string]any), nil
	}

	item, ok := configArray[index].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%w %d is a %T, expected an object", errTheConfigEntryAt, index, configArray[index])
	}

	return item, nil
}

// shouldHandleRecursively determines if the configuration should be handled recursively.
func shouldHandleRecursively(prefixKey string, keys []string) bool {
	if prefixKey != "flagsets" || len(keys) < 1 {
		return false
	}

	recursiveKeys := []string{"retrievers", "notifier", "notifiers", "exporters"}

	return slices.Contains(recursiveKeys, keys[0])
}

// handleRecursiveConfig processes recursive configuration for flagsets.
func handleRecursiveConfig(
	keys []string,
	val string,
	configItem map[string]any,
	configArray []any,
	index int,
	configMap map[string]any,
	prefixKey string,
) (map[string]any, error) {
	recursiveKey := strings.Join(keys, "_")

	modifiedNestedConfig, err := loadArrayEnv(recursiveKey, val, configItem)
	if err != nil {
		return configMap, err
	}

	maps0.Copy(configItem, modifiedNestedConfig)

	return updateConfigArray(configArray, index, configItem, configMap, prefixKey)
}

// handleDirectConfig processes direct configuration assignment.
func handleDirectConfig(keys []string,
	val string,
	configItem map[string]any,
	configArray []any,
	index int,
	configMap map[string]any,
	prefixKey string,
) (map[string]any, error) {
	currentMap := configItem

	for i, key := range keys {
		currentMap = ensureMapExists(currentMap, key, i, len(keys)-1)
	}

	lastKey := keys[len(keys)-1]
	value := parseValue(lastKey, keys, val)
	currentMap[lastKey] = value

	return updateConfigArray(configArray, index, configItem, configMap, prefixKey)
}

// ensureMapExists ensures a map exists at the specified key path.
func ensureMapExists(
	currentMap map[string]any,
	key string,
	currentIndex,
	lastIndex int,
) map[string]any {
	next, ok := currentMap[key].(map[string]any)
	if ok {
		return next
	}

	if currentIndex != lastIndex {
		newMap := make(map[string]any)
		currentMap[key] = newMap

		return newMap
	}

	return currentMap
}

// parseValue parses the value based on the key type.
func parseValue(lastKey string, keys []string, v string) any {
	if isArrayValue(lastKey, keys) {
		return parseArrayValue(v)
	}

	return v
}

// isArrayValue determines if the value should be treated as an array.
func isArrayValue(lastKey string, keys []string) bool {
	return (lastKey == "addresses" && len(keys) > 1 && keys[len(keys)-2] == "kafka") ||
		lastKey == "apikeys"
}

// parseArrayValue splits a comma-separated string and trims whitespace.
func parseArrayValue(v string) []string {
	split := strings.Split(v, ",")
	for i, item := range split {
		split[i] = strings.TrimSpace(item)
	}

	return split
}

// updateConfigArray updates the configuration array and returns the modified config map.
func updateConfigArray(configArray []any, index int, configItem, configMap map[string]any,
	prefixKey string) (map[string]any, error) {
	outRange := index >= len(configArray)

	if outRange {
		configArray = expandArray(configArray, index)
	}

	configArray[index] = configItem
	configMap[prefixKey] = configArray

	return configMap, nil
}

// expandArray expands the array to accommodate the new index.
func expandArray(configArray []any, index int) []any {
	blank := index - len(configArray) + 1
	for range blank {
		configArray = append(configArray, make(map[string]any))
	}

	return configArray
}

// parseOtelResourceAttributes parses the OTEL_RESOURCE_ATTRIBUTES environment variable
// and sets the attributes in the koanf configuration.
// The expected format is "key1=value1,key2=value2,...".
func (c *Loader) parseOtelResourceAttributes(attributes string, log *slog.Logger) {
	configMap := c.k.Raw()

	otel, ok := configMap["otel"].(map[string]any)
	if !ok {
		otel = make(map[string]any)
		configMap["otel"] = otel
	}

	resource, ok := otel["resource"].(map[string]any)
	if !ok {
		resource = make(map[string]any)
		otel["resource"] = resource
	}

	attrs, ok := resource["attributes"].(map[string]any)
	if !ok {
		attrs = make(map[string]any)
		resource["attributes"] = attrs
	}

	for i, attr := range strings.Split(attributes, ",") {
		attrKey, attrVal, found := strings.Cut(attr, "=")
		if !found {
			log.LogAttrs(
				context.Background(),
				slog.LevelError,
				"config: error loading OTEL_RESOURCE_ATTRIBUTES - incorrect format",
				slog.Int("position", i),
			)

			continue
		}

		attrs[attrKey] = attrVal
	}

	_ = c.k.Set("otel", otel)
}
