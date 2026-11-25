package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"strings"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
)

// RedactedValue replaces every value that may be a secret when the configuration is logged.
const RedactedValue = cmdlog.RedactedValue

// opaqueValue replaces values coming from third-party types we can not safely inspect
// (for example the sarama configuration embedded in the Kafka exporter settings).
const opaqueValue = "[OMITTED]"

const (
	modulePathPrefix = "github.com/blackbirdworks/semaphore"
	maxRedactDepth   = 16
)

// sensitiveNamePattern is the fail-closed rule used to decide that a configuration field may hold a secret.
// A field matching it is redacted (whatever its type, including every nested value), unless it has been
// explicitly classified as plain in fieldClassification.
var sensitiveNamePattern = regexp.MustCompile(
	`(?i)key|secret|token|password|passwd|uri|url|dsn|credential|auth|header|cert|private|pass|body`,
)

// SensitiveNamePattern returns the pattern of the field names that are considered as secret-bearing.
func SensitiveNamePattern() *regexp.Regexp { return sensitiveNamePattern }

// secretField / plainField are the 2 possible classifications of a configuration field.
const (
	secretField = true
	plainField  = false
)

// fieldClassification classifies the configuration fields (key: "<package>.<Type>.<Field>").
//   - secretField: the field holds a secret (or may embed credentials), it is never logged.
//   - plainField: the field name looks sensitive but its content is not a secret (a file path, a key name, ...).
//
// A field whose name matches sensitiveNamePattern MUST be listed here (a test enforces it), this way adding a
// field named "xxxToken" forces the author to take a decision. If a new field is not listed it is redacted anyway.
func fieldClassification() map[string]bool {
	return map[string]bool{
		// Config
		"config.ValidateFlagsEndpoint.MaxYAMLBodySize": plainField, // sizes
		"config.ValidateFlagsEndpoint.MaxTOMLBodySize": plainField,
		"config.ValidateFlagsEndpoint.MaxJSONBodySize": plainField,
		"config.Config.DisableVersionHeader":           plainField, // a boolean
		"config.Config.AuthorizedKeys":                 secretField,
		"config.Config.APIKeys":                        secretField,
		// FlagSet
		"config.FlagSet.APIKeys": secretField,
		// APIKeys (admin / evaluation keys)
		// Studio
		"config.Studio.URL":     secretField, // pre-signed URLs carry credentials in the query string
		"config.Studio.Headers": secretField, // typically an Authorization header
		// OFREP event stream
		"config.OfrepEventStream.BaseURL": secretField, // may embed credentials (user info)
		// Notifiers
		"config.NotifierConf.SlackWebhookURL": secretField, // the Slack webhook URL is itself the secret
		"config.NotifierConf.EndpointURL":     secretField,
		"config.NotifierConf.Secret":          secretField,
		"config.NotifierConf.Headers":         secretField,
		"config.NotifierConf.WebhookURL":      secretField,
		// Exporters
		"config.ExporterConf.EndpointURL":       secretField,
		"config.ExporterConf.Secret":            secretField,
		"config.ExporterConf.Headers":           secretField,
		"config.ExporterConf.QueueURL":          secretField,
		"config.ExporterConf.GoogleCredentials": secretField,
		"config.ExporterConf.AccountKey":        secretField,
		"config.ExporterConf.SecretArn":         secretField,
		// Retrievers
		"retrieverconf.RetrieverConf.GithubToken":             secretField,
		"retrieverconf.RetrieverConf.URL":                     secretField,
		"retrieverconf.RetrieverConf.HTTPBody":                secretField, // the request body may carry credentials
		"retrieverconf.RetrieverConf.HTTPHeaders":             secretField,
		"retrieverconf.RetrieverConf.BaseURL":                 secretField,
		"retrieverconf.RetrieverConf.AuthToken":               secretField,
		"retrieverconf.RetrieverConf.URI":                     secretField, // MongoDB / PostgreSQL connection strings
		"retrieverconf.RetrieverConf.AccountKey":              secretField,
		"retrieverconf.RetrieverConf.GitHubAppPrivateKey":     secretField,
		"retrieverconf.RetrieverConf.HTTPClientKeyPath":       plainField, // a file path
		"retrieverconf.RetrieverConf.HTTPClientCertPath":      plainField, // a file path
		"retrieverconf.RetrieverConf.HTTPCACertPath":          plainField, // a file path
		"retrieverconf.RetrieverConf.GitHubAppPrivateKeyFile": plainField, // a file path
		"retrieverconf.RetrieverConf.Key":                     plainField, // name of the key inside a ConfigMap
		"retrieverconf.SerializableRedisOptions.Password":     secretField,
	}
}

// IsSecretField returns true if the field must never be logged.
// "typeName" is the "<package>.<Type>" of the struct holding the field.
func IsSecretField(typeName, fieldName string) bool {
	if secret, classified := fieldClassification()[typeName+"."+fieldName]; classified {
		return secret
	}

	return sensitiveNamePattern.MatchString(fieldName)
}

// Redacted returns a copy of the configuration, as generic maps and slices, in which every secret is replaced
// by RedactedValue. It is the only representation of the configuration that should be logged.
func (c *Config) Redacted() map[string]any {
	if c == nil {
		return nil
	}

	c.mutex.RLock()
	defer c.mutex.RUnlock()

	out, _ := redactValue(reflect.ValueOf(c), 0).(map[string]any)

	return out
}

// LogValue implements [slog.LogValuer], so [slog.Any]("config", conf) never leaks secrets.
func (c *Config) LogValue() slog.Value {
	return slog.AnyValue(c.Redacted())
}

// String implements [fmt.Stringer], so "%v" of a *Config never leaks secrets.
func (c *Config) String() string {
	b, err := json.Marshal(c.Redacted())
	if err != nil {
		return RedactedValue
	}

	return string(b)
}

// GoString implements [fmt.GoStringer], so "%#v" of a *Config never leaks secrets.
func (c *Config) GoString() string { return c.String() }

func isOwnType(t reflect.Type) bool {
	if t.Name() == "" { // anonymous struct declared inside one of our types
		return true
	}

	return strings.HasPrefix(t.PkgPath(), modulePathPrefix)
}

func koanfName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("koanf"), ",")
	if name == "" {
		name, _, _ = strings.Cut(field.Tag.Get("mapstructure"), ",")
	}

	if name == "" {
		name = field.Name
	}

	return name
}

func redactStruct(val reflect.Value, depth int) map[string]any {
	t := val.Type()
	typeName := t.String()

	out := make(map[string]any, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		// embedded (inline / squash) structs are flattened, like the configuration file is.
		if field.Anonymous && !IsSecretField(typeName, field.Name) {
			if inner, ok := redactValue(val.Field(i), depth+1).(map[string]any); ok {
				maps.Copy(out, inner)

				continue
			}
		}

		if IsSecretField(typeName, field.Name) {
			if isZeroValue(val.Field(i)) {
				out[koanfName(field)] = nil
			} else {
				out[koanfName(field)] = RedactedValue
			}

			continue
		}

		out[koanfName(field)] = redactValue(val.Field(i), depth+1)
	}

	return out
}

func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan, reflect.Func, reflect.Interface, reflect.Pointer, reflect.String, reflect.Struct, reflect.UnsafePointer:
		return v.IsZero()
	default:
		return v.IsZero()
	}
}

// redactValue converts a value to a generic structure that is safe to log.
func redactValue(val reflect.Value, depth int) any {
	if !val.IsValid() || depth > maxRedactDepth {
		return nil
	}

	switch val.Kind() {
	case reflect.Pointer, reflect.Interface:
		if val.IsNil() {
			return nil
		}

		return redactValue(val.Elem(), depth+1)
	case reflect.Struct:
		if !isOwnType(val.Type()) {
			return opaqueValue
		}

		return redactStruct(val, depth)
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, val.Len())
		for i := range val.Len() {
			out = append(out, redactValue(val.Index(i), depth+1))
		}

		return out
	case reflect.Map:
		out := make(map[string]any, val.Len())

		iter := val.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			if sensitiveNamePattern.MatchString(key) {
				out[key] = RedactedValue

				continue
			}

			out[key] = redactValue(iter.Value(), depth+1)
		}

		return out
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return opaqueValue
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String:
		if val.CanInterface() {
			return val.Interface()
		}

		return opaqueValue
	default:
		if val.CanInterface() {
			return val.Interface()
		}

		return opaqueValue
	}
}
