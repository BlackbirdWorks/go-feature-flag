package connection_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// TestFactory_AWSConfigIsACopy mutates one part of a returned configuration per case and checks a second call
// does not see it. Each case fails if its part were shared with the cached configuration.
func TestFactory_AWSConfigIsACopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(*aws.Config)
		check  func(aws.Config) any
		want   any
		name   string
	}{
		{
			name:   "BaseEndpoint pointee",
			mutate: func(c *aws.Config) { *c.BaseEndpoint = "http://tampered.test" },
			check:  func(c aws.Config) any { return aws.ToString(c.BaseEndpoint) },
			want:   testEndpoint,
		},
		{
			name:   "BaseEndpoint pointer",
			mutate: func(c *aws.Config) { c.BaseEndpoint = aws.String("http://replaced.test") },
			check:  func(c aws.Config) any { return aws.ToString(c.BaseEndpoint) },
			want:   testEndpoint,
		},
		{
			name:   "APIOptions element",
			mutate: func(c *aws.Config) { c.APIOptions[0] = nil },
			check:  func(c aws.Config) any { return c.APIOptions[0] != nil },
			want:   true,
		},
		{
			name:   "APIOptions rewritten in place",
			mutate: func(c *aws.Config) { c.APIOptions = append(c.APIOptions[:0], nil) },
			check:  func(c aws.Config) any { return c.APIOptions[0] != nil },
			want:   true,
		},
		{
			name: "APIOptions length",
			mutate: func(c *aws.Config) {
				c.APIOptions = slices.Insert(c.APIOptions, 0, nil)
			},
			check: func(c aws.Config) any { return len(c.APIOptions) },
			want:  1,
		},
		{
			name:   "Region",
			mutate: func(c *aws.Config) { c.Region = "moon-1" },
			check:  func(c aws.Config) any { return c.Region },
			want:   "us-east-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New(connection.WithObserver(&recorder{}))
			require.NoError(t, fac.Set("a", testS3()))

			first, err := fac.AWSConfig(t.Context(), "a")
			require.NoError(t, err)

			tt.mutate(&first)

			second, err := fac.AWSConfig(t.Context(), "a")
			require.NoError(t, err)
			assert.Equal(t, tt.want, tt.check(second))
		})
	}
}

// TestFactory_AWSConfigSharesOnlyWhatItShould guards the cases above against a new pointer, slice or map field
// of aws.Config that the factory would set and leave shared: only the HTTP client and the credentials provider
// (the pool and the cache of the connection) and the retryer constructor may be the same in two copies.
func TestFactory_AWSConfigSharesOnlyWhatItShould(t *testing.T) {
	t.Parallel()

	fac := connection.New(connection.WithObserver(&recorder{}))
	require.NoError(t, fac.Set("a", testS3()))

	first, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)

	second, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)

	shared := map[string]bool{"HTTPClient": true, "Credentials": true, "Retryer": true}
	valueA, valueB := reflect.ValueOf(first), reflect.ValueOf(second)

	checked := 0

	for i := range valueA.NumField() {
		field := valueA.Type().Field(i)
		fieldA, fieldB := valueA.Field(i), valueB.Field(i)

		kind := fieldA.Kind()
		if kind != reflect.Pointer && kind != reflect.Map && kind != reflect.Slice {
			continue
		}

		if fieldA.IsNil() || shared[field.Name] {
			continue
		}

		checked++

		assert.NotEqual(t, fieldA.UnsafePointer(), fieldB.UnsafePointer(),
			"%s aliases the cached configuration", field.Name)
	}

	assert.Equal(t, 2, checked, "BaseEndpoint and APIOptions are the pointer and slice fields set by the factory")
}
