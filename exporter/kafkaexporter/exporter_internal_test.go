package kafkaexporter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	abcdef = "ABCDEF"
)

var (
	errDialError            = errors.New("dial error")
	errFailureToSendMessage = errors.New("failure to send message: datacenter on fire")
)

type messageSenderMock struct {
	error    error
	messages []*sarama.ProducerMessage
}

func (s *messageSenderMock) SendMessages(msgs []*sarama.ProducerMessage) error {
	s.messages = append(s.messages, msgs...)

	return s.error
}

func TestExporter_IsBulk(t *testing.T) {
	t.Parallel()

	exp := Exporter{}
	assert.False(t, exp.IsBulk(), "DeprecatedExporterV1 is not a bulk exporter")
}

func TestExporter_Export(t *testing.T) {
	t.Parallel()

	const mockTopic = "mockTopic"

	tests := []struct {
		dialer   func(addrs []string, config *sarama.Config) (MessageSender, error)
		name     string
		format   string
		settings Settings
		events   []exporter.ExportableEvent
		wantErr  bool
	}{
		{
			name:    "should receive an error if dial failed",
			format:  protocol.FormatJSON,
			wantErr: true,
			settings: Settings{
				Topic:     mockTopic,
				Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
			},
			dialer: func(_ []string, _ *sarama.Config) (MessageSender, error) {
				return nil, errDialError
			},
		},
		{
			name:    "should use default dialer when none provided",
			format:  protocol.FormatJSON,
			wantErr: true, // The default dialer should error
			settings: Settings{
				Topic:     mockTopic,
				Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
			},
			dialer: nil,
		},
		{
			name:    "should receive an event with a valid feature event",
			format:  protocol.FormatJSON,
			wantErr: false,
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
				exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      abcdef,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
			},
			settings: Settings{
				Topic:     mockTopic,
				Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
			},
			dialer: func(_ []string, _ *sarama.Config) (MessageSender, error) {
				return &messageSenderMock{}, nil
			},
		},
		{
			name:    "should default to JSON format if none provided",
			format:  "", // Should default to JSON and generate a valid message
			wantErr: false,
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
				exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      abcdef,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
			},
			settings: Settings{
				Topic:     mockTopic,
				Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
			},
			dialer: func(_ []string, _ *sarama.Config) (MessageSender, error) {
				return &messageSenderMock{}, nil
			},
		},
		{
			name:    "should return an error if the publisher is returning an error",
			format:  protocol.FormatJSON,
			wantErr: true,
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
				exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      abcdef,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
			},
			settings: Settings{
				Topic:     mockTopic,
				Addresses: []string{fixtures.AddressOne, fixtures.AddressTwo},
				Config:    &sarama.Config{},
			},
			dialer: func(_ []string, _ *sarama.Config) (MessageSender, error) {
				return &messageSenderMock{
					error: errFailureToSendMessage,
				}, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := &Exporter{
				Format:   tt.format,
				Settings: tt.settings,
				dialer:   tt.dialer,
			}

			logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

			err := exp.Export(context.Background(), logger, tt.events)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)

			want := make([]*sarama.ProducerMessage, len(tt.events))
			for index, event := range tt.events {
				messageBody, _ := json.Marshal(event)
				want[index] = &sarama.ProducerMessage{
					Topic: mockTopic,
					Key:   sarama.StringEncoder(event.GetUserKey()),
					Value: sarama.ByteEncoder(messageBody),
				}
			}

			sender, ok := exp.sender.(*messageSenderMock)
			require.True(t, ok)
			assert.Equal(t, want, sender.messages)
		})
	}
}
