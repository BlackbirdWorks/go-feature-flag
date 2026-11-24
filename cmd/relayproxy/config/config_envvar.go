package config

import (
	"errors"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/knadh/koanf/maps"
	"github.com/knadh/koanf/v2"
	"go.uber.org/zap"
)

// environProvider is koanf's env provider reading the variables from environ instead of os.Environ, so
// the tests do not have to change the environment of the process.
type environProvider struct {
	prefix  string
	environ func() []string
	cb      func(key, value string) (string, any)
	// skip (optional) tells the variables to leave out, by the name without the prefix and the value.
	skip func(key, value string) bool
	// skipped (optional) receives the names of the variables left out.
	skipped func(names []string)
}

// ReadBytes is not supported by the environment provider.
func (p *environProvider) ReadBytes() ([]byte, error) {
	return nil, errors.New("env provider does not support this method")
}

// Read returns the variables that start with the prefix, mapped with the callback, as a nested map.
func (p *environProvider) Read() (map[string]any, error) {
	mp := make(map[string]any)
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
		mp[key] = mapped
	}
	if p.skipped != nil && len(skipped) > 0 {
		slices.Sort(skipped)
		p.skipped(skipped)
	}
	return maps.Unflatten(mp, "."), nil
}

var (
	// serviceLinkPortRegexp matches PORT_<port>_<PROTO> and its _ADDR, _PORT and _PROTO variants.
	serviceLinkPortRegexp = regexp.MustCompile(`^PORT_[0-9]+_(TCP|UDP|SCTP)(_ADDR|_PORT|_PROTO)?$`)
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

func (c *ConfigLoader) mapEnvVariablesProvider(prefix string, log *zap.Logger) koanf.Provider {
	return &environProvider{prefix: prefix, environ: c.environ(),
		skip: func(key, value string) bool { return prefix != "" && isServiceLink(key, value) },
		skipped: func(names []string) {
			// only the names are logged, the values may be anything.
			log.Debug("config: ignoring Kubernetes service link variables", zap.Strings("names", names))
		},
		cb: func(key, v string) (string, any) {
			key = strings.TrimPrefix(key, prefix)
			switch {
			case strings.HasPrefix(key, "RETRIEVERS"),
				strings.HasPrefix(key, "NOTIFIER"),
				strings.HasPrefix(key, "NOTIFIERS"),
				strings.HasPrefix(key, "FLAGSETS"),
				strings.HasPrefix(key, "EXPORTERS"):
				configMap := c.k.Raw()
				modifiedConfigMap, err := loadArrayEnv(key, v, configMap)
				if err != nil {
					log.Error(
						"config: error loading array env",
						zap.String("key", key),
						// the value of the variable may be a secret (API key, token, ...): only log its size.
						zap.Int("valueLength", len(v)),
						zap.Error(err),
					)
					return key, v
				}
				// Update the global config with the modified configMap
				for configKey, configValue := range modifiedConfigMap {
					_ = c.k.Set(configKey, configValue)
				}
				return key, v
			case strings.HasSuffix(key, "KAFKA_ADDRESSES"),
				strings.HasSuffix(key, "APIKEYS"),
				strings.HasPrefix(key, "AUTHORIZEDKEYS_EVALUATION"),
				strings.HasPrefix(key, "AUTHORIZEDKEYS_ADMIN"):
				transformedKey := strings.ReplaceAll(strings.ToLower(key), "_", ".")
				return transformedKey, strings.Split(v, ",")
			case key == "OTEL_RESOURCE_ATTRIBUTES":
				c.parseOtelResourceAttributes(v, log)
				return key, v
			default:
				return strings.ReplaceAll(strings.ToLower(key), "_", "."), v
			}
		}}
}

// environ returns the source of the environment variables, os.Environ unless the loader was given another one.
func (c *ConfigLoader) environ() func() []string {
	if c.environFunc != nil {
		return c.environFunc
	}
	return os.Environ
}

// Load the ENV Like:RETRIEVERS_0_HEADERS_AUTHORIZATION
func loadArrayEnv(s, v string, configMap map[string]any) (map[string]any, error) {
	paths := normalizePaths(s)
	prefixKey := paths[0]

	configArray, ok := configMap[prefixKey].([]any)
	if !ok {
		// Initialize the array if it doesn't exist
		configArray = []any{}
		configMap[prefixKey] = configArray
	}

	index, err := strconv.Atoi(paths[1])
	if err != nil {
		return configMap, err
	}

	configItem := fetchOrInitConfigItemAtIndex(configArray, index)
	keys := paths[2:]

	if shouldHandleRecursively(prefixKey, keys) {
		return handleRecursiveConfig(keys, v, configItem, configArray, index, configMap, prefixKey)
	}

	return handleDirectConfig(keys, v, configItem, configArray, index, configMap, prefixKey)
}

// normalizePaths splits the input string and converts all parts to lowercase
func normalizePaths(s string) []string {
	paths := strings.Split(s, "_")
	for i, str := range paths {
		paths[i] = strings.ToLower(str)
	}
	return paths
}

// fetchOrInitConfigItemAtIndex retrieves or creates a config item at the specified index
func fetchOrInitConfigItemAtIndex(configArray []any, index int) map[string]any {
	outRange := index > len(configArray)-1
	if outRange {
		return make(map[string]any)
	}
	return configArray[index].(map[string]any)
}

// shouldHandleRecursively determines if the configuration should be handled recursively
func shouldHandleRecursively(prefixKey string, keys []string) bool {
	if prefixKey != "flagsets" || len(keys) < 1 {
		return false
	}

	recursiveKeys := []string{"retrievers", "notifier", "notifiers", "exporters"}
	return slices.Contains(recursiveKeys, keys[0])
}

// handleRecursiveConfig processes recursive configuration for flagsets
func handleRecursiveConfig(
	keys []string,
	v string,
	configItem map[string]any,
	configArray []any,
	index int,
	configMap map[string]any,
	prefixKey string,
) (map[string]any, error) {
	recursiveKey := strings.Join(keys, "_")
	modifiedNestedConfig, err := loadArrayEnv(recursiveKey, v, configItem)
	if err != nil {
		return configMap, err
	}

	for k, val := range modifiedNestedConfig {
		configItem[k] = val
	}

	return updateConfigArray(configArray, index, configItem, configMap, prefixKey)
}

// handleDirectConfig processes direct configuration assignment
func handleDirectConfig(keys []string,
	v string,
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
	value := parseValue(lastKey, keys, v)
	currentMap[lastKey] = value

	return updateConfigArray(configArray, index, configItem, configMap, prefixKey)
}

// ensureMapExists ensures a map exists at the specified key path
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

// parseValue parses the value based on the key type
func parseValue(lastKey string, keys []string, v string) any {
	if isArrayValue(lastKey, keys) {
		return parseArrayValue(v)
	}
	return v
}

// isArrayValue determines if the value should be treated as an array
func isArrayValue(lastKey string, keys []string) bool {
	return (lastKey == "addresses" && len(keys) > 1 && keys[len(keys)-2] == "kafka") ||
		lastKey == "apikeys"
}

// parseArrayValue splits a comma-separated string and trims whitespace
func parseArrayValue(v string) []string {
	split := strings.Split(v, ",")
	for i, item := range split {
		split[i] = strings.TrimSpace(item)
	}
	return split
}

// updateConfigArray updates the configuration array and returns the modified config map
func updateConfigArray(configArray []any, index int, configItem, configMap map[string]any,
	prefixKey string) (map[string]any, error) {
	outRange := index > len(configArray)-1

	if outRange {
		configArray = expandArray(configArray, index)
	}

	configArray[index] = configItem
	configMap[prefixKey] = configArray

	return configMap, nil
}

// expandArray expands the array to accommodate the new index
func expandArray(configArray []any, index int) []any {
	blank := index - len(configArray) + 1
	for i := 0; i < blank; i++ {
		configArray = append(configArray, make(map[string]any))
	}
	return configArray
}

// parseOtelResourceAttributes parses the OTEL_RESOURCE_ATTRIBUTES environment variable
// and sets the attributes in the koanf configuration.
// The expected format is "key1=value1,key2=value2,..."
func (c *ConfigLoader) parseOtelResourceAttributes(attributes string, log *zap.Logger) {
	configMap := c.k.Raw()
	otel, ok := configMap["otel"].(map[string]any)
	if !ok {
		configMap["otel"] = make(map[string]any)
		otel = configMap["otel"].(map[string]any)
	}

	resource, ok := otel["resource"].(map[string]any)
	if !ok {
		otel["resource"] = make(map[string]any)
		resource = otel["resource"].(map[string]any)
	}

	attrs, ok := resource["attributes"].(map[string]any)
	if !ok {
		resource["attributes"] = make(map[string]any)
		attrs = resource["attributes"].(map[string]any)
	}

	for i, attr := range strings.Split(attributes, ",") {
		k, v, found := strings.Cut(attr, "=")
		if !found {
			log.Error("config: error loading OTEL_RESOURCE_ATTRIBUTES - incorrect format",
				zap.Int("position", i))
			continue
		}

		attrs[k] = v
	}

	_ = c.k.Set("otel", otel)
}
