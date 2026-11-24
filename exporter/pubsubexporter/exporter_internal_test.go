package pubsubexporter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"cloud.google.com/go/pubsub" //nolint:staticcheck // SA1019: the v1 pubsub exporter is under test
	"cloud.google.com/go/pubsub/pstest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errNotExpectedNumberOf      = errors.New("not expected number of options")
	errUnexpectedOptionProvided = errors.New("unexpected option provided")
	errErroredClient            = errors.New("errored client")
)

func TestExporter_Export(t *testing.T) {
	t.Parallel()

	const (
		projectID = "fake-project"
		topic     = "fake-topic"
	)

	ctx := context.TODO()
	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	type clientFactory = func(context.Context, string, ...option.ClientOption) (*pubsub.Client, error)

	type fields struct {
		projectID             string
		topic                 string
		options               []option.ClientOption
		publishSettings       *pubsub.PublishSettings
		enableMessageOrdering bool
		newClientFunc         func(client *pubsub.Client) clientFactory
	}
	tests := []struct {
		name    string
		fields  fields
		events  []exporter.ExportableEvent
		wantErr bool
	}{
		{
			name: "should publish a single message with the feature event",
			fields: fields{
				topic: topic,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         "feature",
					ContextKind:  "anonymousUser",
					UserKey:      "ABCD",
					CreationDate: 1617970547,
					Key:          "random-key",
					Variation:    "Default",
					Value:        "YO",
					Default:      false,
				},
			},
		},
		{
			name: "should publish multiple messages with feature events",
			fields: fields{
				topic: topic,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         "feature1",
					ContextKind:  "anonymousUser1",
					UserKey:      "ABCD",
					CreationDate: 1617970547,
					Key:          "random-key1",
					Variation:    "Default",
					Value:        "YO",
					Default:      false,
				},
				exporter.FeatureEvent{
					Kind:         "feature2",
					ContextKind:  "anonymousUser2",
					UserKey:      "ABCDEF",
					CreationDate: 1617970527,
					Key:          "random-key2",
					Variation:    "Default",
					Value:        "YO",
					Default:      true,
				},
			},
		},
		{
			name: "should use the provided client options when creating a client",
			fields: fields{
				topic:   topic,
				options: []option.ClientOption{option.WithAPIKey("some-api-key")},
				newClientFunc: func(client *pubsub.Client) clientFactory {
					return func(_ context.Context, _ string, opts ...option.ClientOption) (*pubsub.Client, error) {
						if len(opts) != 1 {
							return nil, errNotExpectedNumberOf
						} else if opts[0] != option.WithAPIKey("some-api-key") {
							return nil, errUnexpectedOptionProvided
						}

						return client, nil
					}
				},
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         "feature",
					ContextKind:  "anonymousUser",
					UserKey:      "ABCD",
					CreationDate: 1617970547,
					Key:          "random-key",
					Variation:    "Default",
					Value:        "YO",
					Default:      false,
				},
			},
		},
		{
			name: "should use the provided publisher settings for the PubSub topic",
			fields: fields{
				topic:           topic,
				publishSettings: &pubsub.PublishSettings{CountThreshold: 123},
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         "feature",
					ContextKind:  "anonymousUser",
					UserKey:      "ABCD",
					CreationDate: 1617970547,
					Key:          "random-key",
					Variation:    "Default",
					Value:        "YO",
					Default:      false,
				},
			},
		},
		{
			name: "should enable message ordering if the configuration is set",
			fields: fields{
				topic:                 topic,
				enableMessageOrdering: true,
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         "feature",
					ContextKind:  "anonymousUser",
					UserKey:      "ABCD",
					CreationDate: 1617970547,
					Key:          "random-key",
					Variation:    "Default",
					Value:        "YO",
					Default:      false,
				},
			},
		},
		{
			name: "should return an error if there is a problem with creating a PubSub client",
			fields: fields{
				topic: topic,
				newClientFunc: func(*pubsub.Client) clientFactory {
					return func(context.Context, string, ...option.ClientOption) (*pubsub.Client, error) {
						return nil, errErroredClient
					}
				},
			},
			wantErr: true,
		},
		{
			name: "should return an error if publishing a message fails",
			fields: fields{
				topic: "not-existing-topic",
			},
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         "feature",
					ContextKind:  "anonymousUser",
					UserKey:      "ABCD",
					CreationDate: 1617970547,
					Key:          "random-key",
					Variation:    "Default",
					Value:        "YO",
					Default:      false,
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server, client := newTestClient(ctx, t, projectID, topic)
			newClientFunc := func(_ context.Context, _ string, _ ...option.ClientOption) (*pubsub.Client, error) {
				return client, nil
			}
			if tt.fields.newClientFunc != nil {
				newClientFunc = tt.fields.newClientFunc(client)
			}

			e := &Exporter{
				ProjectID:             tt.fields.projectID,
				Topic:                 tt.fields.topic,
				Options:               tt.fields.options,
				PublishSettings:       tt.fields.publishSettings,
				EnableMessageOrdering: tt.fields.enableMessageOrdering,
				newClientFunc:         newClientFunc,
			}
			err := e.Export(ctx, logger, tt.events)

			if tt.wantErr {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)
			assertMessages(t, tt.events, server.Messages())
			assertPublisherSettings(t, tt.fields.publishSettings, e.publisher)
			assert.Equal(t, tt.fields.enableMessageOrdering, e.publisher.EnableMessageOrdering)
		})
	}
}

func TestExporter_IsBulk(t *testing.T) {
	t.Parallel()

	e := &Exporter{}

	assert.False(t, e.IsBulk(), "PubSub exporter is not a bulk one")
}

func assertMessages(
	t *testing.T,
	expectedEvents []exporter.ExportableEvent,
	messages []*pstest.Message,
) {
	t.Helper()
	events := make([]exporter.FeatureEvent, len(messages))
	for i, message := range messages {
		assert.Equal(t, map[string]string{"emitter": "GO Feature Flag"}, message.Attributes,
			"message should have associated emitter attribute")

		var event exporter.FeatureEvent
		err := json.Unmarshal(message.Data, &event)
		require.NoError(t, err)

		events[i] = event
	}
	assert.ElementsMatchf(t, expectedEvents, events, "events should match in any order")
}

func assertPublisherSettings(
	t *testing.T,
	expectedSettings *pubsub.PublishSettings,
	publisher *pubsub.Topic,
) {
	t.Helper()
	if expectedSettings != nil {
		assert.Equal(t, *expectedSettings, publisher.PublishSettings)
	} else {
		assert.Equal(t, pubsub.DefaultPublishSettings, publisher.PublishSettings)
	}
}

// newTestClient starts an in-memory PubSub server (one per test case, so cases can run
// in parallel without seeing each other's messages) and a client connected to it.
func newTestClient(
	ctx context.Context,
	t *testing.T,
	projectID, topic string,
) (*pstest.Server, *pubsub.Client) {
	t.Helper()

	server := pstest.NewServer()
	t.Cleanup(func() { server.Close() })

	conn, err := grpc.NewClient(
		server.Addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client, err := pubsub.NewClient(ctx, projectID, option.WithGRPCConn(conn))
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	_, err = client.CreateTopic(ctx, topic)
	require.NoError(t, err)

	return server, client
}
