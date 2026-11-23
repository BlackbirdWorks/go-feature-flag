package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"testing"

	zaplogfmt "github.com/jsternberg/zap-logfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		out[t.String()+"."+f.Name] = f
		ownStructFields(f.Type, seen, out)
	}
}

func allConfigFields() map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	ownStructFields(reflect.TypeOf(Config{}), map[reflect.Type]bool{}, out)
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
		_, classified := fieldClassification[name]
		assert.Truef(t, classified,
			"field %s looks sensitive: classify it in fieldClassification (secretField or plainField)", name)
	}
	for name := range fieldClassification {
		f, exists := fields[name]
		assert.Truef(t, exists, "fieldClassification has an entry for the unknown field %s", name)
		if exists {
			assert.Truef(t, sensitiveNamePattern.MatchString(f.Name),
				"field %s does not look sensitive, remove it from fieldClassification", name)
		}
	}
}

// fillSecrets sets a recognisable marker in every leaf of v.
func fillLeaves(v reflect.Value, n *int) {
	switch v.Kind() {
	case reflect.String:
		*n++
		v.SetString(fmt.Sprintf("%s-%d", secretMarker, *n))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillLeaves(v.Elem(), n)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillLeaves(v.Index(0), n)
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key := reflect.New(v.Type().Key()).Elem()
		fillLeaves(key, n)
		val := reflect.New(v.Type().Elem()).Elem()
		fillLeaves(val, n)
		v.SetMapIndex(key, val)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillLeaves(v.Field(i), n)
			}
		}
	default:
	}
}

// fillSecretFields puts a marker in every secret field of the struct tree rooted at v.
func fillSecretFields(v reflect.Value, n *int, filled map[string]bool) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		v = v.Elem()
	}
	if v.Kind() == reflect.Slice {
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		fillSecretFields(v.Index(0), n, filled)
		return
	}
	if v.Kind() != reflect.Struct || !isOwnType(v.Type()) {
		return
	}
	for i := 0; i < v.NumField(); i++ {
		f := v.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		if IsSecretField(v.Type().String(), f.Name) {
			fillLeaves(v.Field(i), n)
			filled[v.Type().String()+"."+f.Name] = true
			continue
		}
		fillSecretFields(v.Field(i), n, filled)
	}
}

func newConfigWithSecrets(t *testing.T) *Config {
	t.Helper()
	conf := &Config{}
	n := 0
	filled := map[string]bool{}
	fillSecretFields(reflect.ValueOf(conf), &n, filled)

	// every secret field of the configuration must have been filled with a marker.
	for name, secret := range fieldClassification {
		if secret {
			assert.Truef(t, filled[name], "the test did not fill the secret field %s", name)
		}
	}

	// values that are not in a secret field but are in a map under a sensitive key.
	conf.EvaluationContextEnrichment = map[string]any{"apiKey": secretMarker + "-enrichment", "env": "prod"}
	conf.OtelConfig.Resource.Attributes = map[string]string{"service.token": secretMarker + "-otel"}
	conf.Version = "visible-version"
	conf.LogLevel = "debug"
	return conf
}

func newEncoder(encoding string, cfg zapcore.EncoderConfig) zapcore.Encoder {
	if encoding == "json" {
		return zapcore.NewJSONEncoder(cfg)
	}
	// the logfmt encoder used by cmdhelpers/log when logFormat is "logfmt".
	return zaplogfmt.NewEncoder(cfg)
}

func TestRedactedNeverContainsSecrets(t *testing.T) {
	t.Parallel()
	conf := newConfigWithSecrets(t)
	require.Contains(t, fmt.Sprintf("%#v", *conf.FlagSets[0].Retriever), secretMarker, "the fixture must hold secrets")

	loggers := map[string]string{"json": "json", "logfmt": "logfmt"}
	for name, encoding := range loggers {
		t.Run("zap "+name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			cfg := zap.NewProductionConfig()
			cfg.Encoding = encoding
			cfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
			cfg.Encoding = "json" // the core is replaced below, it only needs a valid base config.
			logger, err := cfg.Build(zap.WrapCore(func(zapcore.Core) zapcore.Core {
				return zapcore.NewCore(newEncoder(encoding, cfg.EncoderConfig), zapcore.AddSync(&buf), zap.DebugLevel)
			}))
			require.NoError(t, err)
			// same call as the flagset manager does when a new configuration is invalid.
			logger.Debug("invalid configuration:", zap.Stringer("newConfig", conf))
			require.NoError(t, logger.Sync())

			assert.NotContains(t, buf.String(), secretMarker)
			assert.Contains(t, buf.String(), "visible-version", "the non-secret part of the config must be logged")
			assert.Contains(t, buf.String(), RedactedValue)
		})
	}

	t.Run("zap.Any", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&buf), zap.DebugLevel)
		zap.New(core).Debug("config", zap.Any("config", conf))
		assert.NotContains(t, buf.String(), secretMarker)
		assert.Contains(t, buf.String(), "visible-version")
	})

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
		assert.Contains(t, buf.String(), "visible-version")
	})
}

func TestRedactedKeepsPlainValues(t *testing.T) {
	t.Parallel()
	conf := &Config{LogLevel: "debug", Version: "1.0"}
	conf.AuthorizedKeys.Admin = []string{"a-secret-admin"}
	redacted := conf.Redacted()
	assert.Equal(t, "debug", redacted["loglevel"])
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
