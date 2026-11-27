package bootstrap_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const enrichmentPath = "evaluation.contextEnrichment"

func enrichmentDoc(body string) string {
	return "version: 2\nevaluation:\n  contextEnrichment:\n" + body
}

// The enrichment is free-form: objects, lists and scalars at any depth are accepted by the loader and the schema, while
// the rest of the file stays strict.
func TestContextEnrichmentIsFreeForm(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../.schema/bootstrap-schema.json")
	require.NoError(t, err)

	checker, root, err := newSchemaChecker(raw)
	require.NoError(t, err)

	tests := []struct {
		want map[string]any
		name string
		doc  string
	}{
		{name: "scalars", doc: enrichmentDoc("    region: eu\n    tier: 3\n    beta: true\n    ratio: 1.5\n"),
			want: map[string]any{"region": "eu", "tier": 3, "beta": true, "ratio": 1.5}},
		{name: "object and list", doc: enrichmentDoc("    company: {size: 120}\n    labels: [beta, pro]\n"),
			want: map[string]any{"company": map[string]any{"size": 120}, "labels": []any{"beta", "pro"}}},
		{
			name: "nested list of objects",
			doc:  enrichmentDoc("    teams:\n      - {name: a, tags: [x]}\n      - {name: b}\n"),
			want: map[string]any{"teams": []any{
				map[string]any{"name": "a", "tags": []any{"x"}}, map[string]any{"name": "b"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			loaded, schemaOK := verdicts(t, checker, root, tt.doc)
			assert.True(t, loaded, "the loader accepts it")
			assert.True(t, schemaOK, "the schema accepts it")

			cfg, parseErr := bootstrap.Parse([]byte(tt.doc), nil, options(nil, nil, false))
			require.NoError(t, parseErr)
			assert.Equal(t, tt.want, cfg.Evaluation.ContextEnrichment)

			_, jsonErr := json.Marshal(cfg.Evaluation.ContextEnrichment)
			require.NoError(t, jsonErr, "what the loader accepts is encodable as JSON")
		})
	}
}

func TestContextEnrichmentRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr  error
		name     string
		doc      string
		wantPath string
	}{
		{name: "duplicate key at depth", doc: enrichmentDoc("    a:\n      b: 1\n      b: 2\n"),
			wantPath: enrichmentPath + ".a.b", wantErr: bootstrap.ErrDuplicateName},
		{name: "unknown key elsewhere stays an error", doc: "version: 2\nevaluation:\n  bogus: 1\n",
			wantPath: "evaluation.bogus", wantErr: bootstrap.ErrUnknownKey},
		{
			name:     "infinity",
			doc:      enrichmentDoc("    a: .inf\n"),
			wantPath: enrichmentPath + ".a",
			wantErr:  bootstrap.ErrInvalidValue,
		},
		{name: "minus infinity at depth", doc: enrichmentDoc("    a:\n      b: [1, -.inf]\n"),
			wantPath: enrichmentPath + ".a.b[1]", wantErr: bootstrap.ErrInvalidValue},
		{name: "not a number", doc: enrichmentDoc("    a: {b: .nan}\n"), wantPath: enrichmentPath + ".a.b",
			wantErr: bootstrap.ErrInvalidValue},
		{name: "a timestamp", doc: enrichmentDoc("    since: 2001-12-14\n"), wantPath: enrichmentPath + ".since",
			wantErr: bootstrap.ErrInvalidValue},
		{name: "binary", doc: enrichmentDoc("    blob: !!binary aGVsbG8=\n"), wantPath: enrichmentPath + ".blob",
			wantErr: bootstrap.ErrInvalidValue},
		{name: "non string key", doc: enrichmentDoc("    a:\n      1: x\n"),
			wantPath: enrichmentPath + ".a.1", wantErr: bootstrap.ErrInvalidValue},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := bootstrap.Parse([]byte(tt.doc), nil, options(nil, nil, false))
			require.Error(t, err)
			assert.NotNil(t, findError(t, err, tt.wantErr, tt.wantPath))
		})
	}
}
