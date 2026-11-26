package ffclient_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
)

const ownedValueFlags = `
object-flag:
  variations:
    Default:
      theme: light
      limits:
        max: 3
      tags:
        - a
  defaultRule:
    variation: Default
  metadata:
    owner: team-b
array-flag:
  variations:
    Default:
      - a
      - nested:
          k: v
  defaultRule:
    variation: Default
disabled-flag:
  variations:
    Default:
      theme: light
  defaultRule:
    variation: Default
  disable: true
  metadata:
    owner: team-a
`

// asMap and asSlice read a fixture container without failing the goroutine of the caller.
func asMap(t *testing.T, v any) map[string]any {
	t.Helper()

	got, ok := v.(map[string]any)
	assert.True(t, ok)

	return got
}

func asSlice(t *testing.T, v any) []any {
	t.Helper()

	got, ok := v.([]any)
	assert.True(t, ok)

	return got
}

func newOwnedValueClient(t *testing.T) *ffclient.GoFeatureFlag {
	t.Helper()

	path := filepath.Join(t.TempDir(), "flags.yaml")
	require.NoError(t, os.WriteFile(path, []byte(ownedValueFlags), 0o600))

	client, err := ffclient.New(&ffclient.Config{
		PollingInterval: time.Hour,
		Retriever:       &fileretriever.Retriever{Path: path},
	})
	require.NoError(t, err)

	t.Cleanup(client.Close)

	return client
}

// the object or array a caller receives belongs to it: changing it changes neither the next evaluation nor the
// flag, and the evaluations of a flag that overlap with those changes do not race.
func TestJSONVariation_ReturnedValueBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	user := ffcontext.NewEvaluationContext("user")

	tests := []struct {
		want     any
		evaluate func(client *ffclient.GoFeatureFlag) any
		mutate   func(t *testing.T, value any)
		name     string
	}{
		{
			name: "object",
			evaluate: func(client *ffclient.GoFeatureFlag) any {
				v, err := client.JSONVariation("object-flag", user, nil)
				assert.NoError(t, err)

				return v
			},
			mutate: func(t *testing.T, value any) {
				t.Helper()

				obj := asMap(t, value)

				obj["theme"] = "dark"
				asMap(t, obj["limits"])["max"] = 99
				asSlice(t, obj["tags"])[0] = "z"
				obj["added"] = true
			},
			want: map[string]any{
				"theme":  "light",
				"limits": map[string]any{"max": 3},
				"tags":   []any{"a"},
			},
		},
		{
			name: "array",
			evaluate: func(client *ffclient.GoFeatureFlag) any {
				v, err := client.JSONArrayVariation("array-flag", user, nil)
				assert.NoError(t, err)

				return v
			},
			mutate: func(t *testing.T, value any) {
				t.Helper()

				arr := asSlice(t, value)

				arr[0] = "z"
				asMap(t, asMap(t, arr[1])["nested"])["k"] = "changed"
			},
			want: []any{"a", map[string]any{"nested": map[string]any{"k": "v"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newOwnedValueClient(t)

			first := tt.evaluate(client)
			assert.Equal(t, tt.want, first)

			tt.mutate(t, first)

			assert.Equal(t, tt.want, tt.evaluate(client), "the next evaluation returns the value that was changed")

			start := make(chan struct{})

			var wg sync.WaitGroup

			for range 8 {
				wg.Go(func() {
					<-start

					for range 100 {
						tt.mutate(t, tt.evaluate(client))
					}
				})
			}

			close(start)
			wg.Wait()

			assert.Equal(t, tt.want, tt.evaluate(client))
		})
	}
}

// the metadata of an evaluation belongs to the caller too, on the disabled path and on the type mismatch path:
// changing what the caller received leaves the flag and the next evaluations as they were.
func TestVariation_ReturnedMetadataBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	user := ffcontext.NewEvaluationContext("user")

	tests := []struct {
		evaluate     func(client *ffclient.GoFeatureFlag) (map[string]any, error)
		wantMetadata map[string]any
		name         string
		flagKey      string
		wantErr      bool
	}{
		{
			name:    "disabled flag",
			flagKey: "disabled-flag",
			evaluate: func(client *ffclient.GoFeatureFlag) (map[string]any, error) {
				res, err := client.RawVariation("disabled-flag", user, nil)

				return res.Metadata, err
			},
			wantMetadata: map[string]any{"owner": "team-a"},
		},
		{
			name:    "type mismatch",
			flagKey: "object-flag",
			evaluate: func(client *ffclient.GoFeatureFlag) (map[string]any, error) {
				res, err := client.BoolVariationDetails("object-flag", user, false)

				return res.Metadata, err
			},
			wantMetadata: map[string]any{"owner": "team-b"},
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newOwnedValueClient(t)

			for range 3 {
				metadata, err := tt.evaluate(client)
				require.Equal(t, tt.wantErr, err != nil)
				require.Equal(t, tt.wantMetadata, metadata)

				metadata["semaphore_cacheable"] = true
				delete(metadata, "owner")
			}

			flags, err := client.GetFlagsFromCache()
			require.NoError(t, err)
			assert.Equal(t, tt.wantMetadata, flags[tt.flagKey].GetMetadata())
		})
	}
}

// a flag that is not there answers the default value of the caller: nil stays nil.
func TestJSONVariation_MissingFlagReturnsTheDefault(t *testing.T) {
	t.Parallel()

	user := ffcontext.NewEvaluationContext("user")

	tests := []struct {
		evaluate func(client *ffclient.GoFeatureFlag) (any, error)
		want     any
		name     string
	}{
		{
			name: "object, nil default",
			evaluate: func(client *ffclient.GoFeatureFlag) (any, error) {
				v, err := client.JSONVariation("missing", user, nil)

				return v, err
			},
			want: map[string]any(nil),
		},
		{
			name: "object, default",
			evaluate: func(client *ffclient.GoFeatureFlag) (any, error) {
				v, err := client.JSONVariation("missing", user, map[string]any{"a": 1})

				return v, err
			},
			want: map[string]any{"a": 1},
		},
		{
			name: "array, nil default",
			evaluate: func(client *ffclient.GoFeatureFlag) (any, error) {
				v, err := client.JSONArrayVariation("missing", user, nil)

				return v, err
			},
			want: []any(nil),
		},
		{
			name: "array, default",
			evaluate: func(client *ffclient.GoFeatureFlag) (any, error) {
				v, err := client.JSONArrayVariation("missing", user, []any{"a"})

				return v, err
			},
			want: []any{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.evaluate(newOwnedValueClient(t))
			require.Error(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// captureExporter keeps the events it exports.
type captureExporter struct {
	events []exporter.ExportableEvent
	mu     sync.Mutex
}

func (*captureExporter) IsBulk() bool { return true }

func (e *captureExporter) Export(_ context.Context, _ *slog.Logger, events []exporter.ExportableEvent) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.events = append(e.events, events...)

	return nil
}

// the event the exporter keeps and the value the caller received are two copies: changing the result after the
// evaluation changes neither the cache nor the event that is queued or exported.
func TestJSONVariation_EventValueIsNotTheValueOfTheCaller(t *testing.T) {
	t.Parallel()

	user := ffcontext.NewEvaluationContext("user")

	tests := []struct {
		evaluate  func(client *ffclient.GoFeatureFlag) any
		mutate    func(t *testing.T, value any)
		wantEvent any
		name      string
	}{
		{
			name: "object",
			evaluate: func(client *ffclient.GoFeatureFlag) any {
				v, _ := client.JSONVariation("object-flag", user, nil)

				return v
			},
			mutate: func(t *testing.T, value any) {
				t.Helper()

				asMap(t, value)["theme"] = "dark"
			},
			wantEvent: map[string]any{
				"theme":  "light",
				"limits": map[string]any{"max": 3},
				"tags":   []any{"a"},
			},
		},
		{
			name: "array",
			evaluate: func(client *ffclient.GoFeatureFlag) any {
				v, _ := client.JSONArrayVariation("array-flag", user, nil)

				return v
			},
			mutate: func(t *testing.T, value any) {
				t.Helper()

				asSlice(t, value)[0] = "z"
			},
			wantEvent: []any{"a", map[string]any{"nested": map[string]any{"k": "v"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "flags.yaml")
			require.NoError(t, os.WriteFile(path, []byte(ownedValueFlags), 0o600))

			exp := &captureExporter{}
			client, err := ffclient.New(&ffclient.Config{
				PollingInterval: time.Hour,
				Retriever:       &fileretriever.Retriever{Path: path},
				DataExporter:    ffclient.DataExporter{Exporter: exp, FlushInterval: time.Hour, MaxEventInMemory: 50},
			})
			require.NoError(t, err)

			tt.mutate(t, tt.evaluate(client))

			// closing the client flushes the events.
			client.Close()

			exp.mu.Lock()
			defer exp.mu.Unlock()

			require.Len(t, exp.events, 1)

			event, ok := exp.events[0].(*exporter.FeatureEvent)
			require.True(t, ok)
			assert.Equal(t, tt.wantEvent, event.Value)
		})
	}
}
