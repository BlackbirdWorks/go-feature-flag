package ffclient_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ffclient "github.com/blackbirdworks/semaphore"
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

// the metadata of an evaluation belongs to the caller too, on the disabled path as on the others.
func TestRawVariation_ReturnedMetadataBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	client := newOwnedValueClient(t)
	user := ffcontext.NewEvaluationContext("user")

	want := map[string]any{"owner": "team-a"}

	for range 3 {
		res, err := client.RawVariation("disabled-flag", user, nil)
		require.NoError(t, err)
		assert.Equal(t, want, res.Metadata)

		res.Metadata["semaphore_cacheable"] = true
		delete(res.Metadata, "owner")
	}

	flags, err := client.GetFlagsFromCache()
	require.NoError(t, err)
	assert.Equal(t, want, flags["disabled-flag"].GetMetadata())
}

// the type mismatch answer carries the metadata of the flag, as a copy.
func TestVariationDetails_TypeMismatchMetadataBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	client := newOwnedValueClient(t)
	user := ffcontext.NewEvaluationContext("user")

	want := map[string]any{"owner": "team-b"}

	for range 3 {
		res, err := client.BoolVariationDetails("object-flag", user, false)
		require.Error(t, err)
		require.Equal(t, want, res.Metadata)

		res.Metadata["semaphore_cacheable"] = true
		delete(res.Metadata, "owner")
	}

	flags, err := client.GetFlagsFromCache()
	require.NoError(t, err)
	assert.Equal(t, want, flags["object-flag"].GetMetadata())
}

// a flag that is not there answers the default value of the caller: nil stays nil.
func TestJSONVariation_MissingFlagReturnsTheDefault(t *testing.T) {
	t.Parallel()

	client := newOwnedValueClient(t)
	user := ffcontext.NewEvaluationContext("user")

	object, err := client.JSONVariation("missing", user, nil)
	require.Error(t, err)
	assert.Nil(t, object)

	object, err = client.JSONVariation("missing", user, map[string]any{"a": 1})
	require.Error(t, err)
	assert.Equal(t, map[string]any{"a": 1}, object)

	array, err := client.JSONArrayVariation("missing", user, nil)
	require.Error(t, err)
	assert.Nil(t, array)

	array, err = client.JSONArrayVariation("missing", user, []any{"a"})
	require.Error(t, err)
	assert.Equal(t, []any{"a"}, array)
}
