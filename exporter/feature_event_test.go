package exporter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
)

const (
	aBCD                                = "ABCD"
	anonymousUser                       = "anonymousUser"
	feature                             = "feature"
	boolType                            = "bool"
	variationDefault                    = "Default"
	intType                             = "int"
	stringType                          = "string"
	float2                              = "float"
	kindFeatureContextKindAnonymousUser = `{"kind":"feature","contextKind":"anonymousUser","u` +
		`serKey":"ABCD","creationDate":1617970547,"key":"ra`
	ndomKeyVariationDefault = `ndom-key","variation":"Default","value":{"bool":true,` +
		`"float":1.23,"int":1,"string":"string"},"default":fals`
	randomKey         = "random-key"
	returnExistingKey = "return existing key"
)

func TestNewFeatureEvent(t *testing.T) {
	t.Parallel()

	type args struct {
		user             ffcontext.Context
		value            any
		exporterMetadata exporter.FeatureEventMetadata
		flagKey          string
		variation        string
		version          string
		source           string
		failed           bool
	}

	tests := []struct {
		want exporter.FeatureEvent
		name string
		args args
	}{
		{
			name: "anonymous user",
			args: args{
				user: ffcontext.NewEvaluationContextBuilder(aBCD).
					AddCustom(anonymous, true).
					Build(),
				flagKey:   randomKey,
				value:     "YO",
				variation: variationDefault,
				failed:    false,
				version:   "",
				source:    sERVER,
			},
			want: exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: clocktest.Instant().Unix(),
				Key:          randomKey,
				Variation:    variationDefault,
				Value:        "YO",
				Default:      false,
				Source:       sERVER,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := clocktest.Instant().Unix()
			got := exporter.NewFeatureEvent(
				tt.args.user,
				tt.args.flagKey,
				tt.args.value,
				tt.args.variation,
				tt.args.failed,
				tt.args.version,
				tt.args.source,
				tt.args.exporterMetadata,
			)
			after := clocktest.Instant().Unix()

			// The creation date is the wall clock at call time: check the window, then compare the rest.
			assert.GreaterOrEqual(t, got.CreationDate, before)
			assert.LessOrEqual(t, got.CreationDate, after)

			want := tt.want
			want.CreationDate = got.CreationDate
			assert.Equalf(
				t,
				want,
				got,
				"NewFeatureEvent(%v, %v, %v, %v, %v, %v, %v)",
				tt.args.user,
				tt.args.flagKey,
				tt.args.value,
				tt.args.variation,
				tt.args.failed,
				tt.args.version,
				tt.args.source,
			)
		})
	}
}

func TestFeatureEvent_MarshalInterface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		featureEvent *exporter.FeatureEvent
		want         *exporter.FeatureEvent
		name         string
		wantErr      bool
	}{
		{
			name: "happy path",
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value: map[string]any{
					stringType: stringType,
					boolType:   true,
					float2:     1.23,
					intType:    1,
				},
				Default: false,
			},
			want: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value:        `{"bool":true,"float":1.23,"int":1,"string":"string"}`,
				Default:      false,
			},
		},
		{
			name: "marshal failed",
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value:        make(chan int),
				Default:      false,
			},
			wantErr: true,
		},
		{
			name: "nil value",
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value:        nil,
				Default:      false,
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			val, err := tt.featureEvent.ConvertValueForParquet()
			if (err != nil) != tt.wantErr {
				assert.Failf(
					t,
					"unexpected error",
					"FeatureEvent.MarshalInterface() error = %v, wantErr %v",
					err,
					tt.wantErr,
				)

				return
			}

			if tt.want != nil {
				assert.Equal(t, tt.want.Value, val)
			}
		})
	}
}

func TestFeatureEvent_FormatInJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		featureEvent *exporter.FeatureEvent
		wantErr      assert.ErrorAssertionFunc
		name         string
		want         string
	}{
		{
			name: "Should not return a metadata field if metadata is empty",
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value: map[string]any{
					stringType: stringType,
					boolType:   true,
					float2:     1.23,
					intType:    1,
				},
				Default:  false,
				Metadata: map[string]any{},
			},
			want: kindFeatureContextKindAnonymousUser +
				ndomKeyVariationDefault +
				`e,"version":"","source":""}`,
			wantErr: assert.NoError,
		},
		{
			name: "Should not return a metadata field if metadata is nil",
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value: map[string]any{
					stringType: stringType,
					boolType:   true,
					float2:     1.23,
					intType:    1,
				},
				Default: false,
			},
			want: kindFeatureContextKindAnonymousUser +
				ndomKeyVariationDefault +
				`e,"version":"","source":""}`,
			wantErr: assert.NoError,
		},
		{
			name: "Should return a metadata field if metadata is not empty",
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value: map[string]any{
					stringType: stringType,
					boolType:   true,
					float2:     1.23,
					intType:    1,
				},
				Default: false,
				Metadata: map[string]any{
					"metadata1": "metadata1",
					"metadata2": 24,
					"metadata3": true,
				},
			},
			want: kindFeatureContextKindAnonymousUser +
				ndomKeyVariationDefault +
				`e,"version":"","source":"","metadata":{"metadata1":"metadata1","metadata2":24,"metadata3":true}}`,
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.featureEvent.FormatInJSON()
			tt.wantErr(t, err)

			if err == nil {
				assert.JSONEq(t, tt.want, string(got))
			}
		})
	}
}

func TestFeatureEvent_GetKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		featureEvent *exporter.FeatureEvent
		want         string
	}{
		{
			name: returnExistingKey,
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value: map[string]any{
					stringType: stringType,
					boolType:   true,
					float2:     1.23,
					intType:    1,
				},
				Default: false,
			},
			want: randomKey,
		},
		{
			name: "empty key",
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          "",
				Variation:    variationDefault,
				Value:        nil,
				Default:      false,
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.featureEvent.GetKey())
		})
	}
}

func TestFeatureEvent_GetUserKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		featureEvent *exporter.FeatureEvent
		want         string
	}{
		{
			name: returnExistingKey,
			featureEvent: &exporter.FeatureEvent{
				Kind:         feature,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
				Variation:    variationDefault,
				Value: map[string]any{
					stringType: stringType,
					boolType:   true,
					float2:     1.23,
					intType:    1,
				},
				Default: false,
			},
			want: aBCD,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.featureEvent.GetUserKey())
		})
	}
}
