package ffcontext_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/ffcontext"
)

func TestUser_AddCustomAttribute(t *testing.T) {
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

func Test_ExtractGOFFProtectedFields(t *testing.T) {
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
				UsedLegacyKey:   true,
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
				UsedLegacyKey:   true,
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
				UsedLegacyKey:   true,
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
				UsedLegacyKey:   true,
				CurrentDateTime: new(time.Date(2022, 8, 1, 0, 0, 10, 0, time.UTC)),
			},
		},
		{
			name: "context goff specifics nil",
			ctx: ffcontext.NewEvaluationContextBuilder("my-targetingKey").
				AddCustom("gofeatureflag", nil).
				Build(),
			want: ffcontext.GoffContextSpecifics{
				UsedLegacyKey:   true,
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
				UsedLegacyKey:   true,
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
				UsedLegacyKey:   true,
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
				UsedLegacyKey:   true,
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
				UsedLegacyKey: true,
				FlagList:      []string{"flag1", "flag2"},
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
				UsedLegacyKey: true,
				ExporterMetadata: map[string]any{
					"toto": 123,
					"titi": 123.45,
					"tutu": true,
					"tata": "bonjour",
				},
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

func TestEvaluationContext_MarshalJSON(t *testing.T) {
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

func TestEvaluationContext_ToMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		context  ffcontext.EvaluationContext
		expected map[string]any
		name     string
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
