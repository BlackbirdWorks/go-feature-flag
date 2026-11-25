package ffclient

import (
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// Track is used to track an event.
// Note: Use this function only if you are using multiple semaphore instances.
func (g *GoFeatureFlag) Track(
	trackingEventName string,
	ctx ffcontext.EvaluationContext,
	trackingEventDetails exporter.TrackingEventDetails,
) {
	if g != nil && g.trackingEventDataExporter != nil {
		contextKind := "user"
		if ctx.IsAnonymous() {
			contextKind = "anonymousUser"
		}

		event := &exporter.TrackingEvent{
			Kind:              "tracking",
			ContextKind:       contextKind,
			UserKey:           ctx.GetKey(),
			CreationDate:      clock.Now().Unix(),
			Key:               trackingEventName,
			EvaluationContext: ctx.ToMap(),
			TrackingDetails:   trackingEventDetails,
		}
		g.trackingEventDataExporter.AddEvent(event)
	}
}
