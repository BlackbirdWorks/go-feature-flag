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
				&exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
				&exporter.FeatureEvent{
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
				&exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
				&exporter.FeatureEvent{
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
				&exporter.FeatureEvent{
					Kind:         fixtures.KindFeature,
					ContextKind:  fixtures.UserKeyAnonymous,
					UserKey:      fixtures.UserKeyABCD,
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
				&exporter.FeatureEvent{
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

			logger := slog.Default()

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

type closableSender struct {
	err    error
	closes int
}

func (*closableSender) SendMessages([]*sarama.ProducerMessage) error { return nil }

func (c *closableSender) Close() error {
	c.closes++

	return c.err
}

// TestExporter_Close checks that Close closes the producer once, that a second call, a sender that cannot be
// closed and a never initialised exporter do nothing, and that the error of the producer is returned.
var errClose = errors.New("close failed")

func TestExporter_Close(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sender         func() (MessageSender, *closableSender)
		wantErr        error
		name           string
		calls          int
		wantCloseCalls int
	}{
		{
			name: "a producer is closed once", calls: 2, wantCloseCalls: 1,
			sender: func() (MessageSender, *closableSender) {
				sender := &closableSender{}

				return sender, sender
			},
		},
		{name: "an exporter that never exported has nothing to close", calls: 2},
		{
			name: "a sender without Close is left alone", calls: 1,
			sender: func() (MessageSender, *closableSender) { return &plainSender{}, nil },
		},
		{
			name: "the error of the producer is returned", calls: 1, wantErr: errClose, wantCloseCalls: 1,
			sender: func() (MessageSender, *closableSender) {
				sender := &closableSender{err: errClose}

				return sender, sender
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exp := &Exporter{}

			var closable *closableSender

			if tt.sender != nil {
				exp.sender, closable = tt.sender()
			}

			var errs []error

			for range tt.calls {
				errs = append(errs, exp.Close())
			}

			if tt.wantErr != nil {
				require.ErrorIs(t, errs[0], tt.wantErr)
			} else {
				assert.Equal(t, make([]error, tt.calls), errs)
			}

			if closable != nil {
				assert.Equal(t, tt.wantCloseCalls, closable.closes)
			}

			assert.Nil(t, exp.sender, "the producer is dropped: the next Export dials again")
		})
	}
}

type plainSender struct{}

func (*plainSender) SendMessages([]*sarama.ProducerMessage) error { return nil }
