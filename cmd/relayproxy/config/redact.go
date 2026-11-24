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

func koanfName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("koanf"), ",")
	if name == "" {
		name, _, _ = strings.Cut(f.Tag.Get("mapstructure"), ",")
	}
	if name == "" {
		name = f.Name
	}

	return name
}

func redactStruct(v reflect.Value, depth int) map[string]any {
	t := v.Type()
	typeName := t.String()
	out := make(map[string]any, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		// embedded (inline / squash) structs are flattened, like the configuration file is.
		if f.Anonymous && !IsSecretField(typeName, f.Name) {
			if inner, ok := redactValue(v.Field(i), depth+1).(map[string]any); ok {
				maps.Copy(out, inner)

				continue
			}
		}
		if IsSecretField(typeName, f.Name) {
			if isZeroValue(v.Field(i)) {
				out[koanfName(f)] = nil
			} else {
				out[koanfName(f)] = RedactedValue
			}

			continue
		}
		out[koanfName(f)] = redactValue(v.Field(i), depth+1)
	}

	return out
}

func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	default:
		return v.IsZero()
	}
}

// redactValue converts a value to a generic structure that is safe to log.
func redactValue(v reflect.Value, depth int) any {
	if !v.IsValid() || depth > maxRedactDepth {
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}

		return redactValue(v.Elem(), depth+1)
	case reflect.Struct:
		if !isOwnType(v.Type()) {
			return opaqueValue
		}

		return redactStruct(v, depth)
	case reflect.Slice, reflect.Array:
		out := make([]any, 0, v.Len())
		for i := range v.Len() {
			out = append(out, redactValue(v.Index(i), depth+1))
		}

		return out
	case reflect.Map:
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
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
	default:
		if v.CanInterface() {
			return v.Interface()
		}

		return opaqueValue
	}
}
