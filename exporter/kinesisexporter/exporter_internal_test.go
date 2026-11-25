package kinesisexporter

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"
	"github.com/aws/smithy-go/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	testExplicitHashKey = "test-explicit-hash-key"
	testStream          = "test-stream"
	testStreamArn       = "test-stream-arn"
	testStreamName      = "test-stream-name"
)

var (
	errFailureToSendMessage = errors.New("failure to send message: datacenter on fire")
)

func TestExporter_IsBulk(t *testing.T) {
	t.Parallel()

	exp := Exporter{}
	assert.False(t, exp.IsBulk(), "DeprecatedExporterV1 is not a bulk exporter")
}

func TestExporter_ExportBasicWithStreamName(t *testing.T) {
	t.Parallel()

	mock := MockKinesisSender{}
	exp := Exporter{
		Format:   protocol.FormatJSON,
		sender:   &mock,
		Settings: NewSettings(WithStreamName(testStream)),
	}

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	assert.Nil(t, exp.AwsConfig)
	err := exp.Export(
		context.Background(),
		logger,
		[]exporter.ExportableEvent{
			NewFeatureEvent(),
			NewFeatureEvent(),
			NewFeatureEvent(),
		},
	)

	require.NoError(t, err)
	assert.Len(t, mock.PutRecordsInputs, 1)
	assert.Len(t, mock.PutRecordsInputs[0].Records, 3)

	for idx := range mock.PutRecordsInputs {
		assert.Equal(t, mock.PutRecordsInputs[idx].StreamName, exp.Settings.StreamName)
		assert.Equal(t, mock.PutRecordsInputs[idx].StreamARN, exp.Settings.StreamArn)
	}

	assert.NotNil(t, exp.AwsConfig)
	assert.NoError(t, err)
}

func TestExporter_ExportBasicWithStreamArn(t *testing.T) {
	t.Parallel()

	mock := MockKinesisSender{}
	exp := Exporter{
		Format:   protocol.FormatJSON,
		sender:   &mock,
		Settings: NewSettings(WithStreamArn(testStream)),
	}

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	assert.Nil(t, exp.AwsConfig)
	err := exp.Export(
		context.Background(),
		logger,
		[]exporter.ExportableEvent{
			NewFeatureEvent(),
			NewFeatureEvent(),
			NewFeatureEvent(),
		},
	)

	require.NoError(t, err)
	assert.Len(t, mock.PutRecordsInputs, 1)
	assert.Len(t, mock.PutRecordsInputs[0].Records, 3)

	for idx := range mock.PutRecordsInputs {
		assert.Equal(t, mock.PutRecordsInputs[idx].StreamName, exp.Settings.StreamName)
		assert.Equal(t, mock.PutRecordsInputs[idx].StreamARN, exp.Settings.StreamArn)
	}

	assert.NotNil(t, exp.AwsConfig)

	require.NoError(t, err)
	assert.NotNil(t, exp.AwsConfig)
}

func TestExporter_ShouldRaiseErrorIfNoStreamIsSpecified(t *testing.T) {
	t.Parallel()

	mock := MockKinesisSender{}
	exp := Exporter{
		Format: protocol.FormatJSON,
		sender: &mock,
	}

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	assert.Nil(t, exp.AwsConfig)
	err := exp.Export(
		context.Background(),
		logger,
		[]exporter.ExportableEvent{NewFeatureEvent()},
	)

	assert.EqualError(t, err, "send: no StreamName or StreamArn provided")
}

func TestExporter_ExportAWSConfigurationCustomisation(t *testing.T) {
	t.Parallel()

	mock := MockKinesisSender{}

	exp := Exporter{
		Format: protocol.FormatJSON,
		sender: &mock,
		Settings: NewSettings(
			WithStreamName(testStream),
			WithPartitionKey(func(context.Context, exporter.ExportableEvent) string {
				return "test-key"
			}),
		),
		AwsConfig: &aws.Config{
			Region: "unexistent-region",
		},
	}

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	err := exp.Export(
		context.Background(),
		logger,
		[]exporter.ExportableEvent{
			NewFeatureEvent(),
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "test-key", *mock.PutRecordsInputs[0].Records[0].PartitionKey)
	assert.Equal(t, "unexistent-region", exp.AwsConfig.Region)
}

func TestExporter_ExportSenderError(t *testing.T) {
	t.Parallel()

	mock := MockKinesisSenderWithError{}

	exp := Exporter{
		Format:   protocol.FormatJSON,
		sender:   &mock,
		Settings: NewSettings(WithStreamName(testStream)),
	}

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	err := exp.Export(
		context.Background(),
		logger,
		[]exporter.ExportableEvent{
			NewFeatureEvent(),
		},
	)

	assert.EqualError(t, err, "send: failure to send message: datacenter on fire")
}

func TestExporterSettingsCreation(t *testing.T) {
	t.Parallel()

	{
		settings := NewSettings()
		assert.Equal(t, fixtures.FlagsetDefault, settings.PartitionKey(context.TODO(), NewFeatureEvent()))
		assert.Nil(t, settings.StreamName)
		assert.Nil(t, settings.StreamArn)
		assert.Nil(t, settings.ExplicitHashKey)
	}
	{
		settings := NewSettings(WithStreamArn(testStreamArn))
		assert.Equal(t, fixtures.FlagsetDefault, settings.PartitionKey(context.TODO(), NewFeatureEvent()))
		assert.Nil(t, settings.StreamName)
		assert.Equal(t, testStreamArn, *settings.StreamArn)
		assert.Nil(t, settings.ExplicitHashKey)
	}
	{
		settings := NewSettings(WithStreamName(testStreamName))
		assert.Equal(t, fixtures.FlagsetDefault, settings.PartitionKey(context.TODO(), NewFeatureEvent()))
		assert.Equal(t, testStreamName, *settings.StreamName)
		assert.Nil(t, settings.StreamArn)
		assert.Nil(t, settings.ExplicitHashKey)
	}
	{
		settings := NewSettings(WithExplicitHashKey(testExplicitHashKey))
		assert.Equal(t, fixtures.FlagsetDefault, settings.PartitionKey(context.TODO(), NewFeatureEvent()))
		assert.Nil(t, settings.StreamName)
		assert.Nil(t, settings.StreamArn)
		assert.Equal(t, testExplicitHashKey, *settings.ExplicitHashKey)
	}
	{
		settings := NewSettings(
			WithStreamName(testStreamName),
			WithStreamArn(testStreamArn),
			WithExplicitHashKey(testExplicitHashKey),
			WithPartitionKey(
				func(_ context.Context, _ exporter.ExportableEvent) string { return "non-default" },
			),
		)
		assert.Equal(t, "non-default", settings.PartitionKey(context.TODO(), NewFeatureEvent()))
		assert.Nil(t, settings.StreamName) // overwritten by streamArn
		assert.Equal(t, testStreamArn, *settings.StreamArn)
		assert.Equal(t, testExplicitHashKey, *settings.ExplicitHashKey)
	}
	{
		settings := NewSettings(
			WithStreamArn(testStreamArn),
			WithStreamName(testStreamName),
		)
		assert.Nil(t, settings.StreamArn) // overwritten by streamName
		assert.Equal(t, testStreamName, *settings.StreamName)
	}
}

func TestHugeMessageExportFlow(t *testing.T) {
	t.Parallel()

	event := exporter.FeatureEvent{
		Kind:         fixtures.KindFeature,
		ContextKind:  "anonymousUser",
		UserKey:      "ABCD",
		CreationDate: 1617970547,
		Key:          "random-key",
		Variation:    "Default",
		Value:        "YO",
		Default:      false,
	}
	event.Value = string(make([]byte, Mb))

	mock := MockKinesisSender{}

	exp := Exporter{
		Format:   protocol.FormatJSON,
		sender:   &mock,
		Settings: NewSettings(WithStreamName(testStream)),
	}

	logger := &fflog.FFLogger{LeveledLogger: slog.Default()}

	err := exp.Export(
		context.Background(),
		logger,
		[]exporter.ExportableEvent{
			event,
			event,
			event,
			event,
		},
	)

	require.NoError(t, err)
	assert.Empty(t, mock.PutRecordsInputs)
}

func NewFeatureEvent() *exporter.FeatureEvent {
	return &exporter.FeatureEvent{
		Kind:         fixtures.KindFeature,
		ContextKind:  "anonymousUser",
		UserKey:      "ABCD",
		CreationDate: 1617970547,
		Key:          "random-key",
		Variation:    "Default",
		Value:        "YO",
		Default:      false,
	}
}

type MockKinesisSender struct {
	PutRecordsInputs []*kinesis.PutRecordsInput
}

func (k *MockKinesisSender) SendMessages(
	_ context.Context,
	msgs *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	k.PutRecordsInputs = append(k.PutRecordsInputs, msgs)
	failedRecordCount := int32(0)

	results := make([]types.PutRecordsResultEntry, 0, len(msgs.Records))
	for range msgs.Records {
		results = append(results, types.PutRecordsResultEntry{SequenceNumber: aws.String("1")})
	}

	output := kinesis.PutRecordsOutput{
		Records:           results,
		FailedRecordCount: &failedRecordCount,
		EncryptionType:    types.EncryptionTypeNone,
		ResultMetadata:    middleware.Metadata{},
	}

	return &output, nil
}

type MockKinesisSenderWithError struct{}

func (*MockKinesisSenderWithError) SendMessages(
	_ context.Context,
	_ *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	return nil, errFailureToSendMessage
}
