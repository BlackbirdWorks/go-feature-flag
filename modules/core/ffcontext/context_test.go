package ffcontext_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
)

const (
	acme              = "acme"
	attr12            = "attr1"
	attr22            = "attr2"
	company           = "company"
	currentDateTime2  = "currentDateTime"
	exporterMetadata2 = "exporterMetadata"
	flag1             = "flag1"
	flag2             = "flag2"
	flagList2         = "flagList"
	gofeatureflag     = "gofeatureflag"
	legacy            = "legacy"
	myTargetingKey    = "my-targetingKey"
	old2              = "old"
	targetingKey2     = "targetingKey"
	test2             = "test"
	testKey           = "test-key"
	user123           = "user-123"
	value1            = "value1"
)

func TestUserAddCustomAttribute(t *testing.T) {
	t.Parallel()

	type args struct {
		value any
		name  string
	}

	tests := []struct {
		args args
		user ffcontext.EvaluationContext
		want map[string]any
		name string
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
				name:  test2,
				value: test2,
			},
			want: map[string]any{
				test2: test2,
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
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]string{
					currentDateTime2: time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC).
						Format(time.RFC3339),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as map[string]interface and date as time.Time",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					currentDateTime2: time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as map[string]interface and date as *time.Time",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					currentDateTime2: new(
						time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC),
					),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as map[string]interface",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					currentDateTime2: time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC).
						Format(time.RFC3339),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics nil",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, nil).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: nil,
			},
		},
		{
			name: "no context goff specifics",
			ctx:  ffcontext.NewEvaluationContextBuilder(myTargetingKey).Build(),
			want: ffcontext.GoffContextSpecifics{
				CurrentDateTime: nil,
			},
		},
		{
			name: "context goff specifics as GoffContextSpecifics type",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, ffcontext.GoffContextSpecifics{
					CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics as GoffContextSpecifics type contains flagList",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, ffcontext.GoffContextSpecifics{
					CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
					FlagList:        []string{flag1, flag2},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
				FlagList:        []string{flag1, flag2},
			},
		},
		{
			name: "context goff specifics as map[string]interface type contains flagList",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					currentDateTime2: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)).
						Format(time.RFC3339),
					flagList2: []string{flag1, flag2},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
				FlagList:        []string{flag1, flag2},
			},
		},
		{
			name: "context goff specifics only flagList",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					flagList2: []string{flag1, flag2},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey: true,
				FlagList:      []string{flag1, flag2},
			},
		},
		{
			name: "context goff specifics with exporter metadata",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					exporterMetadata2: map[string]any{
						"toto": 123,
						"titi": 123.45,
						"tutu": true,
						"tata": "bonjour",
					},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey: true,
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
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					currentDateTime2: "invalid-date-format",
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: nil,
			},
		},
		{
			name: "context goff specifics with currentDateTime as other type",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					currentDateTime2: 12345,
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
				CurrentDateTime: nil,
			},
		},
		{
			name: "context goff specifics with flagList as []interface{} with non-string elements",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					flagList2: []any{flag1, 123, flag2, true, "flag3"},
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey: true,
				FlagList:      []string{flag1, flag2, "flag3"},
			},
		},
		{
			name: "context goff specifics with flagList as other type",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					flagList2: "not-a-list",
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey: true,
				FlagList:      nil,
			},
		},
		{
			name: "context goff specifics with exporterMetadata as other type",
			ctx: ffcontext.NewEvaluationContextBuilder(myTargetingKey).
				AddCustom(gofeatureflag, map[string]any{
					exporterMetadata2: "not-a-map",
				}).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:    true,
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
			context:  ffcontext.NewEvaluationContext(testKey),
			expected: `{"targetingKey":"test-key","attributes":{}}`,
		},
		{
			name: "marshal with attributes",
			context: ffcontext.NewEvaluationContextBuilder(testKey).
				AddCustom(attr12, value1).
				AddCustom(attr22, 123).
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
		context  ffcontext.EvaluationContext
		expected map[string]any
		name     string
	}{
		{
			name:     "empty attributes",
			context:  ffcontext.NewEvaluationContext(testKey),
			expected: map[string]any{targetingKey2: testKey},
		},
		{
			name: "attributes with values",
			context: ffcontext.NewEvaluationContextBuilder(testKey).
				AddCustom(attr12, value1).
				AddCustom(attr22, 123).
				Build(),
			expected: map[string]any{
				targetingKey2: testKey,
				attr12:        value1,
				attr22:        123,
			},
		},
		{
			name: "attributes with nested map",
			context: ffcontext.NewEvaluationContextBuilder(testKey).
				AddCustom("nested", map[string]any{
					"key1": value1,
					"key2": 42,
				}).
				Build(),
			expected: map[string]any{
				targetingKey2: testKey,
				"nested": map[string]any{
					"key1": value1,
					"key2": 42,
				},
			},
		},
		{
			name: "attributes with nil value",
			context: ffcontext.NewEvaluationContextBuilder(testKey).
				AddCustom(attr12, nil).
				Build(),
			expected: map[string]any{
				targetingKey2: testKey,
				attr12:        nil,
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

		ctx := ffcontext.NewEvaluationContext(user123)
		ctx.AddCustomAttribute(company, acme)

		_ = ctx.ToMap()

		assert.Equal(t, map[string]any{company: acme}, ctx.GetCustom(),
			"ToMap() must not add targetingKey to the context's attributes")
	})

	t.Run("mutating the returned map does not leak back into the context", func(t *testing.T) {
		t.Parallel()

		ctx := ffcontext.NewEvaluationContext(user123)
		ctx.AddCustomAttribute(company, acme)

		m := ctx.ToMap()
		m["injected"] = true

		assert.NotContains(t, ctx.GetCustom(), "injected",
			"mutating the map returned by ToMap() must not reach back into the context")
		assert.Equal(t, map[string]any{company: acme}, ctx.GetCustom())
	})

	t.Run("multiple calls are stable and leave the context untouched", func(t *testing.T) {
		t.Parallel()

		ctx := ffcontext.NewEvaluationContext(user123)
		ctx.AddCustomAttribute(company, acme)

		first := ctx.ToMap()
		second := ctx.ToMap()

		assert.Equal(t, first, second, "successive ToMap() calls must return equal maps")
		assert.Equal(t, map[string]any{company: acme}, ctx.GetCustom())
	})
}

func TestExtractGOFFProtectedFieldsSemaphoreAndLegacyKeys(t *testing.T) {
	t.Parallel()

	newDate := "2025-01-02T03:04:05Z"
	legacyDate := "2020-01-02T03:04:05Z"

	tests := []struct {
		attributes map[string]any
		wantMeta   map[string]any
		name       string
		wantDate   string
		wantFlags  []string
	}{
		{
			name: "semaphore key",
			attributes: map[string]any{
				ffcontext.ReservedKey: map[string]any{flagList2: []any{"a"}, currentDateTime2: newDate},
			},
			wantFlags: []string{"a"},
			wantDate:  newDate,
		},
		{
			name: "legacy key only",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{flagList2: []any{"b"}, currentDateTime2: legacyDate},
			},
			wantFlags: []string{"b"},
			wantDate:  legacyDate,
		},
		{
			name: "both keys, semaphore wins field by field",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{
					flagList2: []any{
						old2,
					},
					currentDateTime2:  legacyDate,
					exporterMetadata2: map[string]any{"k": legacy},
				},
				ffcontext.ReservedKey: map[string]any{flagList2: []any{"new"}, currentDateTime2: newDate},
			},
			wantFlags: []string{"new"},
			wantDate:  newDate,
			wantMeta:  map[string]any{"k": legacy},
		},
		{
			name: "an explicit empty flagList and exporterMetadata of semaphore win over the legacy ones",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{
					flagList2: []any{old2}, exporterMetadata2: map[string]any{"k": legacy},
				},
				ffcontext.ReservedKey: map[string]any{flagList2: []any{}, exporterMetadata2: map[string]any{}},
			},
			wantFlags: []string{},
			wantMeta:  map[string]any{},
		},
		{
			name: "an explicit empty typed flagList of semaphore wins over the legacy one",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{flagList2: []string{old2}},
				ffcontext.ReservedKey:       map[string]any{flagList2: []string{}},
			},
			wantFlags: []string{},
		},
		{
			name: "a flagList of semaphore that is not a list does not hide the legacy one",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]any{flagList2: []any{old2}},
				ffcontext.ReservedKey:       map[string]any{flagList2: "x"},
			},
			wantFlags: []string{old2},
		},
		{
			name: "string maps are read under both keys",
			attributes: map[string]any{
				ffcontext.LegacyReservedKey: map[string]string{currentDateTime2: legacyDate},
				ffcontext.ReservedKey:       map[string]string{flagList2: "ignored-not-a-list"},
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
	assert.True(t, ffcontext.HasReservedKey(map[string]any{gofeatureflag: 1}))
	assert.False(t, ffcontext.HasReservedKey(map[string]any{"x": 1}))
	assert.False(t, ffcontext.HasReservedKey(nil))
}
