package config

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type redactProbe struct {
	hidden string //nolint:unused // read through reflection to reach a value that cannot be interfaced
}

func TestRedactValue(t *testing.T) {
	t.Parallel()

	s := "plain"
	var nilPtr *string
	probe := redactProbe{hidden: "x"}
	tests := []struct {
		name  string
		value reflect.Value
		depth int
		want  any
	}{
		{name: "invalid value", value: reflect.Value{}, want: nil},
		{name: "too deep", value: reflect.ValueOf("deep"), depth: maxRedactDepth + 1, want: nil},
		{name: "nil pointer", value: reflect.ValueOf(nilPtr), want: nil},
		{name: "pointer to a plain value", value: reflect.ValueOf(&s), want: "plain"},
		{name: "third party struct is opaque", value: reflect.ValueOf(time.Now()), want: opaqueValue},
		{name: "function is opaque", value: reflect.ValueOf(func() {}), want: opaqueValue},
		{name: "channel is opaque", value: reflect.ValueOf(make(chan int)), want: opaqueValue},
		{name: "unexported field is opaque", value: reflect.ValueOf(&probe).Elem().Field(0), want: opaqueValue},
		{name: "slice", value: reflect.ValueOf([]string{"a", "b"}), want: []any{"a", "b"}},
		{name: "array", value: reflect.ValueOf([2]int{1, 2}), want: []any{1, 2}},
		{
			name:  "map hides the values of sensitive keys",
			value: reflect.ValueOf(map[string]string{"Authorization": "Bearer abc", "region": "eu"}),
			want:  map[string]any{"Authorization": RedactedValue, "region": "eu"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, redactValue(tt.value, tt.depth))
		})
	}
}

func TestSensitiveNamePattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{"apiKey", true}, {"Password", true}, {"endpointURL", true}, {"X-Authorization", true},
		{"bearerToken", true}, {"privateKeyPath", true}, {"port", false}, {"mode", false}, {"flushInterval", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, SensitiveNamePattern().MatchString(tt.name))
		})
	}
}

func TestConfigStringNeverLeaksSecrets(t *testing.T) {
	t.Parallel()
	c := &Config{CommonFlagSet: CommonFlagSet{}, APIKeys: []string{"super-secret-key"}}
	out := c.String()
	assert.NotContains(t, out, "super-secret-key")
	assert.Contains(t, out, RedactedValue)
}
