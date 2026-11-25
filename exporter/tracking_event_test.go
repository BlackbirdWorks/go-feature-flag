package exporter_test

import (
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
)

const (
	event2          = "event"
	numericValue123 = "123"
	targetingKey    = "targetingKey"
	toto            = "toto"
	tracking        = "tracking"
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
				Kind:              tracking,
				ContextKind:       anonymousUser,
				UserKey:           aBCD,
				CreationDate:      1617970547,
				Key:               randomKey,
				EvaluationContext: map[string]any{targetingKey: aBCD},
				TrackingDetails: map[string]any{
					event2: numericValue123,
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
				Kind:              tracking,
				ContextKind:       anonymousUser,
				UserKey:           aBCD,
				CreationDate:      1617970547,
				Key:               randomKey,
				EvaluationContext: map[string]any{targetingKey: aBCD, toto: 123},
				TrackingDetails: map[string]any{
					event2: numericValue123,
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
				Kind:              tracking,
				ContextKind:       anonymousUser,
				UserKey:           aBCD,
				CreationDate:      1617970547,
				Key:               randomKey,
				EvaluationContext: map[string]any{targetingKey: aBCD},
				TrackingDetails: map[string]any{
					event2: numericValue123,
				},
			},
			want: `{"kind":"tracking","contextKind":"anonymousUser","userKey":"ABCD","creationDate":1617970547,"key":"r` +
				`andom-key","evaluationContext":{"targetingKey":"ABCD"},"trackingEventDetails":{"event":"123"}}`,
			wantErr: assert.NoError,
		},
		{
			name: "Should return a marshalled JSON string of the tracking event with evaluation context attributes",
			trackingEvent: &exporter.TrackingEvent{
				Kind:              tracking,
				ContextKind:       anonymousUser,
				UserKey:           aBCD,
				CreationDate:      1617970547,
				Key:               randomKey,
				EvaluationContext: map[string]any{targetingKey: aBCD, toto: 123},
				TrackingDetails: map[string]any{
					event2: numericValue123,
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
				Kind:         tracking,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
			},
			want: randomKey,
		},
		{
			name: "empty key",
			trackingEvent: &exporter.TrackingEvent{
				Kind:         tracking,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
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
				Kind:         tracking,
				ContextKind:  anonymousUser,
				UserKey:      aBCD,
				CreationDate: 1617970547,
				Key:          randomKey,
			},
			want: aBCD,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.trackingEvent.GetUserKey())
		})
	}
}
