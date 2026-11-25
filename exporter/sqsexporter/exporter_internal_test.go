package sqsexporter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

var (
	errRandomError = errors.New("random error")
)

type SQSSendMessageAPIMock struct {
	messages []sqs.SendMessageInput
}

func (s *SQSSendMessageAPIMock) SendMessage(_ context.Context,
	params *sqs.SendMessageInput,
	_ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	if params.QueueUrl != nil && strings.HasSuffix(*params.QueueUrl, "error") {
		return nil, errRandomError
	}

	s.messages = append(s.messages, *params)

	return &sqs.SendMessageOutput{}, nil
}

func TestSQSIsBulk(t *testing.T) {
	t.Parallel()

	exp := Exporter{}
	assert.False(t, exp.IsBulk(), "DeprecatedExporterV1 is not a bulk exporter")
}

func TestExporterExport(t *testing.T) {
	t.Parallel()

	type fields struct {
		QueueURL   string
		AwsConfig  *aws.Config
		sqsService SQSSendMessageAPIMock
	}

	tests := []struct {
		name    string
		fields  fields
		events  []exporter.ExportableEvent
		wantErr bool
	}{
		{
			name: "should return an error if no QueueURL provided",
			fields: fields{
				// QueueURL:   "https://sqs.eu-west-1.amazonaws.com/XXX/test-queue",
				sqsService: SQSSendMessageAPIMock{},
			},
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
			},
		},
		{
			name: "should receive an event with a valid feature event",
			fields: fields{
				QueueURL:   "https://sqs.eu-west-1.amazonaws.com/XXX/test-queue",
				sqsService: SQSSendMessageAPIMock{},
			},
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
					UserKey:      "ABCDEF",
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
			},
		},
		{
			name: "should return an error if AWS SQS is returning an error",
			fields: fields{
				QueueURL:   "https://sqs.eu-west-1.amazonaws.com/XXX/error",
				sqsService: SQSSendMessageAPIMock{},
			},
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
					UserKey:      "ABCDEF",
					CreationDate: 1617970547,
					Key:          fixtures.UserKeyRandom,
					Variation:    fixtures.VariationDefault,
					Value:        "YO",
					Default:      false,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sqsExp := &Exporter{
				QueueURL:   tt.fields.QueueURL,
				AwsConfig:  tt.fields.AwsConfig,
				sqsService: &tt.fields.sqsService,
			}

			logger := slog.Default()

			err := sqsExp.Export(context.TODO(), logger, tt.events)
			if tt.wantErr {
				assert.Error(t, err)

				return
			}

			require.NoError(t, err)

			want := make([]sqs.SendMessageInput, len(tt.events))
			for index, event := range tt.events {
				messageBody, _ := json.Marshal(event)
				want[index] = sqs.SendMessageInput{
					MessageBody:  aws.String(string(messageBody)),
					QueueUrl:     aws.String(tt.fields.QueueURL),
					DelaySeconds: 0,
					MessageAttributes: map[string]types.MessageAttributeValue{
						"emitter": {
							DataType:    aws.String("String"),
							StringValue: aws.String("GO Feature Flag"),
						},
					},
				}
			}

			assert.Equal(t, want, tt.fields.sqsService.messages)
		})
	}
}
