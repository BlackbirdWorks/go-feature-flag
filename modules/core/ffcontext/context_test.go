package ffcontext_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

func TestUserAddCustomAttribute(t *testing.T) {
	t.Parallel()

	type args struct {
		name  string
		value any
	}
	tests := []struct {
		name string
		user ffcontext.EvaluationContext
		args args
		want map[string]any
	}{
		{
			name: "trying to add nil value",
			user: ffcontext.NewEvaluationContext("123"),
			args: args{},
			want: map[string]any{},
		},
		{
			name: "add valid element",
			user: ffcontext.NewEvaluationContext("123"),
			args: args{
				name:  "test",
				value: "test",
			},
			want: map[string]any{
				"test": "test",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.user.AddCustomAttribute(tt.args.name, tt.args.value)
			assert.Equal(t, tt.want, tt.user.GetCustom())
		})
	}
}

func TestExtractGOFFProtectedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  ffcontext.EvaluationContext
		want ffcontext.GoffContextSpecifics
	}{
		{
			name: "context goff specifics as map[string]string",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]string{
					"currentDateTime": time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC).
						Format(time.RFC3339),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as map[string]interface and date as time.Time",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"currentDateTime": time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as map[string]interface and date as *time.Time",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"currentDateTime": new(
						time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC),
					),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as map[string]interface",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"currentDateTime": time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC).
						Format(time.RFC3339),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics nil",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", nil).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: nil,
			},
		},
		{
			name: "no context goff specifics",
			ctx:  ffcontext.NewEvaluationContextBuilder("my-targetingKey").Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: nil,
			},
		},
		{
			name: "context goff specifics as GoffContextSpecifics type",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", ffcontext.GoffContextSpecifics{
					CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as GoffContextSpecifics type contains flagList",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", ffcontext.GoffContextSpecifics{
					CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
					FlagList:        []string{"flag1", "flag2"},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
				FlagList:        []string{"flag1", "flag2"},
			},
		},
		{
			name: "context goff specifics as map[string]interface type contains flagList",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"currentDateTime": new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)).
						Format(time.RFC3339),
					"flagList": []string{"flag1", "flag2"},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
				FlagList:        []string{"flag1", "flag2"},
			},
		},
		{
			name: "context goff specifics only flagList",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"flagList": []string{"flag1", "flag2"},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				FlagList: []string{"flag1", "flag2"},
			},
		},
		{
			name: "context goff specifics with exporter metadata",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"exporterMetadata": map[string]any{
						"toto": 123,
						"titi": 123.45,
						"tutu": true,
						"tata": "bonjour",
					},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				ExporterMetadata: map[string]any{
					"toto": 123,
					"titi": 123.45,
					"tutu": true,
					"tata": "bonjour",
				},
			},
		},
		{
			name: "context goff specifics with invalid date string",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"currentDateTime": "invalid-date-format",
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: nil,
			},
		},
		{
			name: "context goff specifics with currentDateTime as other type",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"currentDateTime": 12345,
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: nil,
			},
		},
		{
			name: "context goff specifics with flagList as []interface{} with non-string elements",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"flagList": []any{"flag1", 123, "flag2", true, "flag3"},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				FlagList: []string{"flag1", "flag2", "flag3"},
			},
		},
		{
			name: "context goff specifics with flagList as other type",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"flagList": "not-a-list",
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				FlagList: nil,
			},
		},
		{
			name: "context goff specifics with exporterMetadata as other type",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", map[string]any{
					"exporterMetadata": "not-a-map",
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				ExporterMetadata: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.ctx.ExtractGOFFProtectedFields()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEvaluationContextMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		context  ffcontext.EvaluationContext
		expected string
	}{
		{
			name:     "marshal with empty attributes",
			context:  ffcontext.NewEvaluationContext("test-key"),
			expected: `{"targetingKey":"test-key","attributes":{}}`,
		},
		{
			name: "marshal with attributes",
			context: ffcontext.NewEvaluationContextBuilder("test-key").
				AddCustom("attr1", "value1").
				AddCustom("attr2", 123).
				Build(),
			expected: `{"targetingKey":"test-key","attributes":{"attr1":"value1","attr2":123}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data, err := tt.context.MarshalJSON()
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))
		})
	}
}

func TestEvaluationContextToMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		context  ffcontext.EvaluationContext
		expected map[string]any
	}{
		{
			name:     "empty attributes",
			context:  ffcontext.NewEvaluationContext("test-key"),
			expected: map[string]any{"targetingKey": "test-key"},
		},
		{
			name: "attributes with values",
			context: ffcontext.NewEvaluationContextBuilder("test-key").
				AddCustom("attr1", "value1").
				AddCustom("attr2", 123).
				Build(),
			expected: map[string]any{
				"targetingKey": "test-key",
				"attr1":        "value1",
				"attr2":        123,
			},
		},
		{
			name: "attributes with nested map",
			context: ffcontext.NewEvaluationContextBuilder("test-key").
				AddCustom("nested", map[string]any{
					"key1": "value1",
					"key2": 42,
				}).
				Build(),
			expected: map[string]any{
				"targetingKey": "test-key",
				"nested": map[string]any{
					"key1": "value1",
					"key2": 42,
				},
			},
		},
		{
			name: "attributes with nil value",
			context: ffcontext.NewEvaluationContextBuilder("test-key").
				AddCustom("attr1", nil).
				Build(),
			expected: map[string]any{
				"targetingKey": "test-key",
				"attr1":        nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.context.ToMap()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestEvaluationContextToMap_DoesNotMutateContext(t *testing.T) {
	t.Parallel()

	t.Run("targetingKey is not leaked into the context attributes", func(t *testing.T) {
		t.Parallel()

		ctx := ffcontext.NewEvaluationContext("user-123")
		ctx.AddCustomAttribute("company", "acme")

		_ = ctx.ToMap()

		assert.Equal(t, map[string]any{"company": "acme"}, ctx.GetCustom(),
			"ToMap() must not add targetingKey to the context's attributes")
	})

	t.Run("mutating the returned map does not leak back into the context", func(t *testing.T) {
		t.Parallel()

		ctx := ffcontext.NewEvaluationContext("user-123")
		ctx.AddCustomAttribute("company", "acme")

		m := ctx.ToMap()
		m["injected"] = true

		assert.NotContains(t, ctx.GetCustom(), "injected",
			"mutating the map returned by ToMap() must not reach back into the context")
		assert.Equal(t, map[string]any{"company": "acme"}, ctx.GetCustom())
	})

	t.Run("multiple calls are stable and leave the context untouched", func(t *testing.T) {
		t.Parallel()

		ctx := ffcontext.NewEvaluationContext("user-123")
		ctx.AddCustomAttribute("company", "acme")

		first := ctx.ToMap()
		second := ctx.ToMap()

		assert.Equal(t, first, second, "successive ToMap() calls must return equal maps")
		assert.Equal(t, map[string]any{"company": "acme"}, ctx.GetCustom())
	})
}

func TestExtractGOFFProtectedFieldsSemaphoreAndLegacyKeys(t *testing.T) {
	t.Parallel()
	newDate := "2025-01-02T03:04:05Z"
	legacyDate := "2020-01-02T03:04:05Z"
	tests := []struct {
		name       string
		attributes map[string]any
		wantFlags  []string
		wantDate   string
		wantMeta   map[string]any
	}{
		{
			name: "semaphore key",
			attributes: map[string]any{
				ffcontext.ReservedKey: map[string]any{"flagList": []any{"a"}, "currentDateTime": newDate},
			},
			wantFlags: []string{"a"},
			wantDate:  newDate,
		},
		{
			name: "legacy key only",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{"flagList": []any{"b"}, "currentDateTime": legacyDate},
			},
			wantFlags: []string{"b"},
			wantDate:  legacyDate,
		},
		{
			name: "both keys, semaphore wins field by field",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{
					"flagList": []any{
						"old",
					},
					"currentDateTime":  legacyDate,
					"exporterMetadata": map[string]any{"k": "legacy"},
				},
				ffcontext.ReservedKey: map[string]any{"flagList": []any{"new"}, "currentDateTime": newDate},
			},
			wantFlags: []string{"new"},
			wantDate:  newDate,
			wantMeta:  map[string]any{"k": "legacy"},
		},
		{
			name: "an explicit empty flagList and exporterMetadata of semaphore win over the legacy ones",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{
					"flagList": []any{"old"}, "exporterMetadata": map[string]any{"k": "legacy"},
				},
				ffcontext.ReservedKey: map[string]any{"flagList": []any{}, "exporterMetadata": map[string]any{}},
			},
			wantFlags: []string{},
			wantMeta:  map[string]any{},
		},
		{
			name: "an explicit empty typed flagList of semaphore wins over the legacy one",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{"flagList": []string{"old"}},
				ffcontext.ReservedKey:       map[string]any{"flagList": []string{}},
			},
			wantFlags: []string{},
		},
		{
			name: "a flagList of semaphore that is not a list does not hide the legacy one",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{"flagList": []any{"old"}},
				ffcontext.ReservedKey:       map[string]any{"flagList": "x"},
			},
			wantFlags: []string{"old"},
		},
		{
			name: "string maps are read under both keys",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]string{"currentDateTime": legacyDate},
				ffcontext.ReservedKey:       map[string]string{"flagList": "ignored-not-a-list"},
			},
			wantDate: legacyDate,
		},
		{name: "no key", attributes: map[string]any{"other": 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := ffcontext.NewEvaluationContext("key")
			for k, v := range tt.attributes {
				ctx.AddCustomAttribute(k, v)
			}
			got := ctx.ExtractGOFFProtectedFields()
			assert.Equal(t, tt.wantFlags, got.FlagList)
			assert.Equal(t, tt.wantMeta, got.ExporterMetadata)
			switch {
			case tt.wantDate == "":
				assert.Nil(t, got.CurrentDateTime)
			case assert.NotNil(t, got.CurrentDateTime):
				assert.True(t, got.CurrentDateTime.Equal(mustParse(t, tt.wantDate)))
			}
			assert.Equal(t, tt.attributes["other"] == nil, ffcontext.HasReservedKey(tt.attributes))
		})
	}
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse(time.RFC3339, s)
	assert.NoError(t, err)

	return d
}

func TestHasReservedKey(t *testing.T) {
	t.Parallel()
	assert.True(t, ffcontext.HasReservedKey(map[string]any{"semaphore": 1}))
	assert.True(t, ffcontext.HasReservedKey(map[string]any{"gofeatureflag": 1}))
	assert.False(t, ffcontext.HasReservedKey(map[string]any{"x": 1}))
	assert.False(t, ffcontext.HasReservedKey(nil))
}
