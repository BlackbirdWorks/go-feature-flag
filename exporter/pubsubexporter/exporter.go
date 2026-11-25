package pubsubexporter

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub" //nolint:staticcheck // SA1019: the v1 pubsub client is still used by the v1 exporter
	"google.golang.org/api/option"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var _ exporter.Exporter = &Exporter{}

// Exporter publishes events on a PubSub topic.
//
// Deprecated: Use pubsubexporterv2.Exporter instead. This exporter uses the legacy
// cloud.google.com/go/pubsub v1 library. The v2 library provides improved performance
// and additional features. This exporter will be removed in a future version.
type Exporter struct {
	// PublishSettings controls the bundling of published messages.
	// If not set pubsub.DefaultPublishSettings are used.
	PublishSettings *pubsub.PublishSettings
	// newClientFunc is used only for unit testing purposes.
	newClientFunc func(context.Context, string, ...option.ClientOption) (*pubsub.Client, error)
	// publisher facilitates publishing messages on a PubSub topic.
	publisher *pubsub.Topic
	// ProjectID is a project to which the PubSub topic belongs.
	ProjectID string
	// Topic is the name of a topic on which messages will be published.
	Topic string
	// Options are Google Cloud API options to connect to PubSub.
	Options []option.ClientOption
	// EnableMessageOrdering enables the delivery of ordered keys.
	EnableMessageOrdering bool
}

// Export publishes a PubSub message for each exporter.FeatureEvent received.
func (e *Exporter) Export(
	ctx context.Context,
	_ *fflog.FFLogger,
	events []exporter.ExportableEvent,
) error {
	if e.publisher == nil {
		err := e.initPublisher(ctx)
		if err != nil {
			return err
		}
	}

	for _, event := range events {
		messageBody, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal pubsub event: %w", err)
		}

		_, err = e.publisher.Publish(ctx, &pubsub.Message{
			Data:       messageBody,
			Attributes: map[string]string{"emitter": "GO Feature Flag"},
		}).Get(ctx)
		if err != nil {
			return fmt.Errorf("publish pubsub event: %w", err)
		}
	}

	return nil
}

// IsBulk always returns false as PubSub exporter sends each exporter.FeatureEvent as a separate message.
func (e *Exporter) IsBulk() bool {
	return false
}

// initPublisher inits PubSub topic publisher according to the provided configuration.
func (e *Exporter) initPublisher(ctx context.Context) error {
	if e.newClientFunc == nil {
		e.newClientFunc = pubsub.NewClient
	}

	client, err := e.newClientFunc(ctx, e.ProjectID, e.Options...)
	if err != nil {
		return err
	}

	topic := client.Topic(e.Topic)
	if e.PublishSettings != nil {
		topic.PublishSettings = *e.PublishSettings
	}

	topic.EnableMessageOrdering = e.EnableMessageOrdering

	e.publisher = topic

	return nil
}
