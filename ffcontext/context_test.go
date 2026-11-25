package ffcontext_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/ffcontext"
)

const (
	attr12           = "attr1"
	attr22           = "attr2"
	currentDateTime2 = "currentDateTime"
	flag1            = "flag1"
	flag2            = "flag2"
	gofeatureflag    = "gofeatureflag"
	myTargetingKey   = "my-targetingKey"
	targetingKey2    = "targetingKey"
	test2            = "test"
	testKey          = "test-key"
	value1           = "value1"
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

func Test_ExtractGOFFProtectedFields(t *testing.T) {
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
					"flagList": []string{flag1, flag2},
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
					"flagList": []string{flag1, flag2},
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

func TestEvaluationContext_ToMap(t *testing.T) {
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
