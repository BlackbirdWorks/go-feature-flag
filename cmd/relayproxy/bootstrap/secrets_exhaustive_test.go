package bootstrap_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const exhaustiveCanary = "CANARY-exhaustive-5e1f"

// secretLeafKey names a position of a secret by the Go struct that holds it, and whether it is in a list or map.
func secretLeafKey(owner reflect.Type, name string, fieldType reflect.Type) (string, bool) {
	suffix := ""

	for fieldType.Kind() == reflect.Pointer || fieldType.Kind() == reflect.Slice ||
		fieldType.Kind() == reflect.Array || fieldType.Kind() == reflect.Map {
		switch {
		case fieldType.Kind() == reflect.Slice || fieldType.Kind() == reflect.Array:
			suffix = "[]"
		case fieldType.Kind() == reflect.Map:
			suffix = "[*]"
		}

		fieldType = fieldType.Elem()
	}

	if fieldType == reflect.TypeFor[bootstrap.Secret]() || fieldType == reflect.TypeFor[bootstrap.AdminKey]() {
		return owner.Name() + "." + name + suffix, true
	}

	return "", false
}

// findSecretPositions walks the type of the configuration and returns every field that holds a Secret or an
// AdminKey, directly or in a pointer, a list or a map, in any struct reachable from it.
func findSecretPositions(root reflect.Type, seen map[reflect.Type]bool, out map[string]bool) {
	for root.Kind() == reflect.Pointer || root.Kind() == reflect.Slice || root.Kind() == reflect.Array ||
		root.Kind() == reflect.Map {
		root = root.Elem()
	}

	if root.Kind() != reflect.Struct || seen[root] {
		return
	}

	seen[root] = true

	for field := range root.Fields() {
		if !field.IsExported() {
			continue
		}

		if key, isSecret := secretLeafKey(root, field.Name, field.Type); isSecret {
			out[key] = true

			continue
		}

		findSecretPositions(field.Type, seen, out)
	}
}

// secretFixture is a minimal valid document that sets a secret position. The builder gets the name of the
// resource and the YAML of every secret value.
type secretFixture struct {
	build func(resource, value string) string
	name  string
}

func credentialsFixtures(extra string) []secretFixture {
	out := make([]secretFixture, 0, 4)

	for _, connType := range []string{"s3", "kinesis", "sqs", "redshift"} {
		out = append(out, secretFixture{
			name: connType,
			build: func(resource, value string) string {
				return "connections:\n  " + resource + ": {type: " + connType + ", credentials: {" +
					strings.ReplaceAll(extra, "V", value) + "}}\n"
			},
		})
	}

	return out
}

func exporterFixture(kind, keys string) secretFixture {
	return secretFixture{
		name: kind,
		build: func(resource, value string) string {
			return "exporters:\n  " + resource + ": {kind: " + kind + ", " + strings.ReplaceAll(
				keys,
				"V",
				value,
			) + "}\n"
		},
	}
}

func notifierFixture(kind, keys string) secretFixture {
	return secretFixture{
		name: kind,
		build: func(resource, value string) string {
			return "notifiers:\n  " + resource + ": {kind: " + kind + ", " + strings.ReplaceAll(
				keys,
				"V",
				value,
			) + "}\n"
		},
	}
}

// secretRules is the table of fixtures, by position. Adding a Secret to any struct of the configuration fails
// TestEverySecretPositionHasAFixture until a rule is written here.
func secretRules() map[string][]secretFixture {
	static := "type: static, accessKeyId: V, secretAccessKey: V"

	return map[string][]secretFixture{
		"Auth.AdminKeys[]": {
			{name: "admin", build: func(_, value string) string { return "auth: {adminKeys: [" + value + "]}\n" }},
		},
		"Credentials.AccessKeyID":     credentialsFixtures(static),
		"Credentials.SecretAccessKey": credentialsFixtures(static),
		"Credentials.SessionToken":    credentialsFixtures(static + ", sessionToken: V"),
		"Credentials.ExternalID":      credentialsFixtures("type: assumeRole, roleArn: r, externalId: V"),
		"Exporter.EndpointURL":        {exporterFixture("webhook", "endpointUrl: V")},
		"Exporter.Secret":             {exporterFixture("webhook", "endpointUrl: V, secret: V")},
		"Exporter.Headers[*]":         {exporterFixture("webhook", "endpointUrl: V, headers: {A: V, B: V}")},
		"Exporter.GoogleCredentials": {
			exporterFixture("googleStorage", "bucket: b, googleCredentials: V"),
			exporterFixture("pubsub", "projectId: p, topic: t, googleCredentials: V"),
			exporterFixture("bigquery", "projectId: p, datasetId: d, googleCredentials: V"),
		},
		"Exporter.AccountKey": {exporterFixture("azureBlobStorage", "accountName: a, container: c, accountKey: V")},
		"Notifier.WebhookURL": {
			notifierFixture("slack", "webhookUrl: V"), notifierFixture("microsoftteams", "webhookUrl: V"),
			notifierFixture("discord", "webhookUrl: V"),
		},
		"Notifier.EndpointURL": {notifierFixture("webhook", "endpointUrl: V")},
		"Notifier.Secret":      {notifierFixture("webhook", "endpointUrl: V, secret: V")},
		"Notifier.Headers[*]":  {notifierFixture("webhook", "endpointUrl: V, headers: {A: V, B: V}")},
	}
}

// TestEverySecretPositionHasAFixture is the completeness guard: the positions found by reflection and the
// positions that have a fixture are the same set.
func TestEverySecretPositionHasAFixture(t *testing.T) {
	t.Parallel()

	found := map[string]bool{}
	findSecretPositions(reflect.TypeFor[bootstrap.Config](), map[reflect.Type]bool{}, found)

	rules := secretRules()

	assert.ElementsMatch(t, slices.Sorted(maps.Keys(found)), slices.Sorted(maps.Keys(rules)),
		"a Secret field without a fixture rule, or a rule for a field that is not a Secret")

	for key, fixtures := range rules {
		assert.NotEmpty(t, fixtures, key)
	}
}

// secretVariant is how the secret values of a document are written.
type secretVariant struct {
	name   string
	value  string
	inline bool
}

func secretVariants() []secretVariant {
	return []secretVariant{
		{name: "inline", value: exhaustiveCanary, inline: true},
		{name: "fromEnv", value: "{fromEnv: CANARYVAR}"},
		{name: fileReference, value: "{fromFile: /run/canary}"},
	}
}

// exhaustiveOptions resolves every environment variable and every file to the canary, except the override
// variables, which are validated settings.
func exhaustiveOptions(inline bool) bootstrap.LoadOptions {
	return bootstrap.LoadOptions{
		LookupEnv: func(name string) (string, bool) {
			if strings.HasPrefix(name, "SEMAPHORE_") {
				return "", false
			}

			return exhaustiveCanary, true
		},
		ReadFile:           func(string) ([]byte, error) { return []byte(exhaustiveCanary), nil },
		AllowInlineSecrets: inline,
	}
}

// heldSecret is a secret of a loaded configuration and the struct that holds it.
type heldSecret struct {
	holder reflect.Value
	secret bootstrap.Secret
}

// collectSecrets walks a loaded configuration and returns every set secret, with the struct that holds it.
func collectSecrets(value, holder reflect.Value, out *[]heldSecret) {
	switch {
	case value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface:
		if !value.IsNil() {
			collectSecrets(value.Elem(), holder, out)
		}
	case value.Kind() == reflect.Slice || value.Kind() == reflect.Array:
		for i := range value.Len() {
			collectSecrets(value.Index(i), holder, out)
		}
	case value.Kind() == reflect.Map:
		for _, key := range value.MapKeys() {
			collectSecrets(value.MapIndex(key), holder, out)
		}
	case value.Kind() == reflect.Struct:
		collectStruct(value, holder, out)
	}
}

func collectStruct(value, holder reflect.Value, out *[]heldSecret) {
	switch value.Type() {
	case reflect.TypeFor[bootstrap.Secret]():
		secret, _ := reflect.TypeAssert[bootstrap.Secret](value)
		if secret.IsSet() {
			*out = append(*out, heldSecret{holder: holder, secret: secret})
		}
	case reflect.TypeFor[bootstrap.AdminKey]():
		collectStruct(value.Field(0), holder, out)
	default:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				collectSecrets(value.Field(i), value, out)
			}
		}
	}
}

func exhaustiveVerbs() []string {
	return []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d", "%10.3s", "%-20v"}
}

// formatsOf is every way the test knows to turn a value into text.
func formatsOf(tb testing.TB, value any) map[string]string {
	tb.Helper()

	out := map[string]string{}

	for _, verb := range exhaustiveVerbs() {
		out["fmt "+verb] = fmt.Sprintf(verb, value)
	}

	asJSON, err := json.Marshal(value)
	require.NoError(tb, err)

	out["json"] = string(asJSON)

	asYAML, err := yaml.Marshal(value)
	require.NoError(tb, err)

	out["yaml"] = string(asYAML)

	var text, jsonLog bytes.Buffer

	slog.New(slog.NewTextHandler(&text, nil)).Info("value", slog.Any("value", value))
	slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("value", slog.Any("value", value))

	out["slog text"], out["slog json"] = text.String(), jsonLog.String()

	return out
}

// runSecretFixture loads the document, resolves every secret, and checks that the canary is in no text of the
// configuration, of the struct that holds the secret, or of the resolved secret, while Reveal returns it.
func runSecretFixture(t *testing.T, doc string, variant secretVariant) int {
	t.Helper()

	opts := exhaustiveOptions(variant.inline)

	cfg, err := bootstrap.Parse([]byte("version: 2\n"+doc), nil, opts)
	require.NoError(t, err, doc)

	var held []heldSecret

	collectSecrets(reflect.ValueOf(cfg), reflect.ValueOf(cfg).Elem(), &held)
	require.NotEmpty(t, held, "the document sets no secret: %s", doc)

	var printed bytes.Buffer

	require.NoError(t, cfg.Print(&printed))
	assert.NotContains(t, printed.String(), exhaustiveCanary, "Print of:\n%s", doc)

	texts := formatsOf(t, cfg)

	for _, item := range held {
		resolved, resolveErr := item.secret.Resolve(opts)
		require.NoError(t, resolveErr, doc)
		require.Equal(t, exhaustiveCanary, resolved.Reveal(), "the secret was resolved: %s", doc)

		for label, text := range formatsOf(t, resolved) {
			assert.NotContains(t, text, exhaustiveCanary, "resolved secret, %s, of:\n%s", label, doc)
		}

		for label, text := range formatsOf(t, item.holder.Interface()) {
			assert.NotContains(t, text, exhaustiveCanary, "holder %s, %s, of:\n%s", item.holder.Type(), label, doc)
		}
	}

	for label, text := range texts {
		assert.NotContains(t, text, exhaustiveCanary, "configuration, %s, of:\n%s", label, doc)
	}

	return len(held)
}

// TestNoSecretPositionLeaks generates a document for every secret position of every connection type,
// credentials type, exporter kind and notifier kind, in three variants, under several resource names, and
// checks that the resolved value is never printed.
func TestNoSecretPositionLeaks(t *testing.T) {
	t.Parallel()

	resources := []string{"a", "meta", "headers", "secret", "auth", "credentials"}

	var documents int

	for _, key := range slices.Sorted(maps.Keys(secretRules())) {
		for _, fixture := range secretRules()[key] {
			for _, variant := range secretVariants() {
				for _, resource := range resources {
					documents++

					t.Run(key+"/"+fixture.name+"/"+variant.name+"/"+resource, func(t *testing.T) {
						t.Parallel()

						secrets := runSecretFixture(t, fixture.build(resource, variant.value), variant)
						assert.GreaterOrEqual(t, secrets, 1)
					})
				}
			}
		}
	}

	// the number of documents is the product of the table: a table that shrinks fails here
	want := 0
	for _, fixtures := range secretRules() {
		want += len(fixtures) * len(secretVariants()) * len(resources)
	}

	assert.Equal(t, want, documents)
	assert.Greater(t, documents, 300, "the generated corpus")
}
