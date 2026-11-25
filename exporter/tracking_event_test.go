package exporter_test

import (
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	eventName = "event"
)

func TestTrackingEvent_FormatInCSV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		trackingEvent *exporter.TrackingEvent
		wantErr       assert.ErrorAssertionFunc
		name          string
		template      string
		want          string
	}{
		{
			name: "Should return a marshalled JSON string of the tracking event",
			trackingEvent: &exporter.TrackingEvent{
				Kind:              fixtures.Tracking,
				ContextKind:       fixtures.UserKeyAnonymous,
				UserKey:           fixtures.UserKeyABCD,
				CreationDate:      1617970547,
				Key:               fixtures.UserKeyRandom,
				EvaluationContext: map[string]any{fixtures.TargetingKeyAttribute: fixtures.UserKeyABCD},
				TrackingDetails: map[string]any{
					eventName: fixtures.Value123,
				},
			},
			template: `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .EvaluationContext}};{{ .TrackingD` +
				`etails}}`,
			want:    `tracking;anonymousUser;ABCD;1617970547;map[targetingKey:ABCD];map[event:123]`,
			wantErr: assert.NoError,
		},
		{
			name: "Should return a marshalled JSON string of the tracking event with evaluation context attributes",
			trackingEvent: &exporter.TrackingEvent{
				Kind:         fixtures.Tracking,
				ContextKind:  fixtures.UserKeyAnonymous,
				UserKey:      fixtures.UserKeyABCD,
				CreationDate: 1617970547,
				Key:          fixtures.UserKeyRandom,
				EvaluationContext: map[string]any{
					fixtures.TargetingKeyAttribute: fixtures.UserKeyABCD,
					fixtures.Toto:                  123,
				},
				TrackingDetails: map[string]any{
					eventName: fixtures.Value123,
				},
			},
			template: `{{ .Kind}};{{ .ContextKind}};{{ .UserKey}};{{ .CreationDate}};{{ .EvaluationContext}};{{ .TrackingD` +
				`etails}}`,
			want:    `tracking;anonymousUser;ABCD;1617970547;map[targetingKey:ABCD toto:123];map[event:123]`,
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			csvTemplate, err := template.New("test").Parse(tt.template)
			require.NoError(t, err)
			got, err := tt.trackingEvent.FormatInCSV(csvTemplate)
			tt.wantErr(t, err)

			if err == nil {
				assert.Equal(t, tt.want, string(got))
			}
		})
	}
}

func TestTrackingEvent_FormatInJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		trackingEvent *exporter.TrackingEvent
		wantErr       assert.ErrorAssertionFunc
		name          string
		want          string
	}{
		{
			name: "Should return a marshalled JSON string of the tracking event",
			trackingEvent: &exporter.TrackingEvent{
				Kind:              fixtures.Tracking,
				ContextKind:       fixtures.UserKeyAnonymous,
				UserKey:           fixtures.UserKeyABCD,
				CreationDate:      1617970547,
				Key:               fixtures.UserKeyRandom,
				EvaluationContext: map[string]any{fixtures.TargetingKeyAttribute: fixtures.UserKeyABCD},
				TrackingDetails: map[string]any{
					eventName: fixtures.Value123,
				},
			},
			want: `{"kind":"tracking","contextKind":"anonymousUser","userKey":"ABCD","creationDate":1617970547,"key":"r` +
				`andom-key","evaluationContext":{"targetingKey":"ABCD"},"trackingEventDetails":{"event":"123"}}`,
			wantErr: assert.NoError,
		},
		{
			name: "Should return a marshalled JSON string of the tracking event with evaluation context attributes",
			trackingEvent: &exporter.TrackingEvent{
				Kind:         fixtures.Tracking,
				ContextKind:  fixtures.UserKeyAnonymous,
				UserKey:      fixtures.UserKeyABCD,
				CreationDate: 1617970547,
				Key:          fixtures.UserKeyRandom,
				EvaluationContext: map[string]any{
					fixtures.TargetingKeyAttribute: fixtures.UserKeyABCD,
					fixtures.Toto:                  123,
				},
				TrackingDetails: map[string]any{
					eventName: fixtures.Value123,
				},
			},
			want: `{"kind":"tracking","contextKind":"anonymousUser","userKey":"ABCD","creationDate":1617970547,"key":"r` +
				`andom-key","evaluationContext":{"targetingKey":"ABCD","toto":123},"trackingEventDetails":{"event":"123"}}`,
			wantErr: assert.NoError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.trackingEvent.FormatInJSON()
			tt.wantErr(t, err)

			if err == nil {
				assert.JSONEq(t, tt.want, string(got))
			}
		})
	}
}

func TestTrackingEvent_GetKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		trackingEvent *exporter.TrackingEvent
		want          string
	}{
		{
			name: returnExistingKey,
			trackingEvent: &exporter.TrackingEvent{
				Kind:         fixtures.Tracking,
				ContextKind:  fixtures.UserKeyAnonymous,
				UserKey:      fixtures.UserKeyABCD,
				CreationDate: 1617970547,
				Key:          fixtures.UserKeyRandom,
			},
			want: fixtures.UserKeyRandom,
		},
		{
			name: "empty key",
			trackingEvent: &exporter.TrackingEvent{
				Kind:         fixtures.Tracking,
				ContextKind:  fixtures.UserKeyAnonymous,
				UserKey:      fixtures.UserKeyABCD,
				CreationDate: 1617970547,
				Key:          "",
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.trackingEvent.GetKey())
		})
	}
}

func TestTrackingEvent_GetUserKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		trackingEvent *exporter.TrackingEvent
		want          string
	}{
		{
			name: returnExistingKey,
			trackingEvent: &exporter.TrackingEvent{
				Kind:         fixtures.Tracking,
				ContextKind:  fixtures.UserKeyAnonymous,
				UserKey:      fixtures.UserKeyABCD,
				CreationDate: 1617970547,
				Key:          fixtures.UserKeyRandom,
			},
			want: fixtures.UserKeyABCD,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.trackingEvent.GetUserKey())
		})
	}
}
