package config

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	visibleVersion = "visible-version"
)

const secretMarker = "SECRETMARKER"

// ownStructFields walks the type tree of t and returns every exported field of every struct
// of this module, keyed by "<package>.<Type>.<Field>".
func ownStructFields(t reflect.Type, seen map[reflect.Type]bool, out map[string]reflect.StructField) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array ||
		t.Kind() == reflect.Map {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct || !isOwnType(t) || seen[t] {
		return
	}

	seen[t] = true
	for field := range t.Fields() {
		if !field.IsExported() {
			continue
		}

		out[t.String()+"."+field.Name] = field
		ownStructFields(field.Type, seen, out)
	}
}

func allConfigFields() map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	ownStructFields(reflect.TypeFor[Config](), map[reflect.Type]bool{}, out)

	return out
}

// TestEverySensitiveLookingFieldIsClassified fails when a field whose name looks like a secret is added
// to the configuration without taking an explicit decision (secret or plain) in fieldClassification.
func TestEverySensitiveLookingFieldIsClassified(t *testing.T) {
	t.Parallel()

	fields := allConfigFields()
	require.Greater(t, len(fields), 50)

	for name, f := range fields {
		if !sensitiveNamePattern.MatchString(f.Name) {
			continue
		}

		_, classified := fieldClassification()[name]
		assert.Truef(t, classified,
			"field %s looks sensitive: classify it in fieldClassification (secretField or plainField)", name)
	}

	for name := range fieldClassification() {
		f, exists := fields[name]
		assert.Truef(t, exists, "fieldClassification has an entry for the unknown field %s", name)

		if exists {
			assert.Truef(t, sensitiveNamePattern.MatchString(f.Name),
				"field %s does not look sensitive, remove it from fieldClassification", name)
		}
	}
}

// fillLeaves sets a recognisable marker in every leaf of val.
func fillLeaves(target reflect.Value, counter *int) {
	switch target.Kind() {
	case reflect.String:
		*counter++
		target.SetString(fmt.Sprintf("%s-%d", secretMarker, *counter))
	case reflect.Pointer:
		target.Set(reflect.New(target.Type().Elem()))
		fillLeaves(target.Elem(), counter)
	case reflect.Slice:
		target.Set(reflect.MakeSlice(target.Type(), 1, 1))
		fillLeaves(target.Index(0), counter)
	case reflect.Map:
		target.Set(reflect.MakeMap(target.Type()))
		key := reflect.New(target.Type().Key()).Elem()
		fillLeaves(key, counter)

		val := reflect.New(target.Type().Elem()).Elem()
		fillLeaves(val, counter)
		target.SetMapIndex(key, val)
	case reflect.Struct:
		for i := range target.NumField() {
			if target.Type().Field(i).IsExported() {
				fillLeaves(target.Field(i), counter)
			}
		}
	case reflect.Invalid, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.Array, reflect.Chan,
		reflect.Func, reflect.Interface, reflect.UnsafePointer:
		// no string leaf to fill
	}
}

// fillSecretFields puts a marker in every secret field of the struct tree rooted at val.
func fillSecretFields(val reflect.Value, counter *int, filled map[string]bool) {
	for val.Kind() == reflect.Pointer {
		if val.IsNil() {
			val.Set(reflect.New(val.Type().Elem()))
		}

		val = val.Elem()
	}

	if val.Kind() == reflect.Slice {
		val.Set(reflect.MakeSlice(val.Type(), 1, 1))
		fillSecretFields(val.Index(0), counter, filled)

		return
	}

	if val.Kind() != reflect.Struct || !isOwnType(val.Type()) {
		return
	}

	for i := range val.NumField() {
		field := val.Type().Field(i)
		if !field.IsExported() {
			continue
		}

		if IsSecretField(val.Type().String(), field.Name) {
			fillLeaves(val.Field(i), counter)
			filled[val.Type().String()+"."+field.Name] = true

			continue
		}

		fillSecretFields(val.Field(i), counter, filled)
	}
}

func newConfigWithSecrets(t *testing.T) *Config {
	t.Helper()

	conf := &Config{}
	n := 0
	filled := map[string]bool{}
	fillSecretFields(reflect.ValueOf(conf), &n, filled)

	// every secret field of the configuration must have been filled with a marker.
	for name, secret := range fieldClassification() {
		if secret {
			assert.Truef(t, filled[name], "the test did not fill the secret field %s", name)
		}
	}

	// values that are not in a secret field but are in a map under a sensitive key.
	conf.EvaluationContextEnrichment = map[string]any{"apiKey": secretMarker + "-enrichment", "env": "prod"}
	conf.OtelConfig.Resource.Attributes = map[string]string{"service.token": secretMarker + "-otel"}
	conf.Version = visibleVersion
	conf.LogLevel = fixtures.Debug

	return conf
}

func TestRedactedNeverContainsSecrets(t *testing.T) {
	t.Parallel()
	conf := newConfigWithSecrets(t)
	require.Contains(t, fmt.Sprintf("%#v", *conf.FlagSets[0].Retriever), secretMarker, "the fixture must hold secrets")

	for _, format := range []string{"json", "logfmt"} {
		t.Run("logger "+format, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger, err := cmdlog.New(format, conf.LogLevel, cmdlog.WithWriter(&buf))
			require.NoError(t, err)
			// same call as the flagset manager does when a new configuration is invalid.
			logger.Slog.LogAttrs(context.Background(), slog.LevelDebug, "invalid configuration:",
				slog.Any("newConfig", conf))

			assert.NotContains(t, buf.String(), secretMarker)
			assert.Contains(t, buf.String(), visibleVersion, "the non-secret part of the config must be logged")
			assert.Contains(t, buf.String(), RedactedValue)
		})
	}

	t.Run("fmt verbs", func(t *testing.T) {
		t.Parallel()

		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			assert.NotContains(t, fmt.Sprintf(format, conf), secretMarker, format)
		}
	})

	t.Run("slog", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer

		l := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		l.Debug("config", slog.Any("config", conf))
		assert.NotContains(t, buf.String(), secretMarker)
		assert.Contains(t, buf.String(), visibleVersion)
	})
}

func TestRedactedKeepsPlainValues(t *testing.T) {
	t.Parallel()

	conf := &Config{LogLevel: fixtures.Debug, Version: "1.0"}
	conf.AuthorizedKeys.Admin = []string{"a-secret-admin"}
	redacted := conf.Redacted()
	assert.Equal(t, fixtures.Debug, redacted["loglevel"])
	assert.Equal(t, RedactedValue, redacted["authorizedkeys"])
	assert.Nil(t, (*Config)(nil).Redacted())
}

func TestOfrepEventStreamErrorsDoNotContainTheURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{
		"ftp://user:SECRETMARKER-pw@example.com",
		"https://user:SECRETMARKER-pw@example.com?x=1",
		"https://user:SECRETMARKER-pw@exa mple.com",
	} {
		err := OfrepEventStream{BaseURL: baseURL}.IsValid()
		require.Error(t, err, baseURL)
		assert.NotContains(t, err.Error(), secretMarker, baseURL)
	}
}
