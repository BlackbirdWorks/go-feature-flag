package sqsexporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errImpossibleToInitSQS = errors.New("impossible to init SQS exporter: QueueURL is a mandatory parameter")
)

var _ exporter.Exporter = &Exporter{}

// Exporter sends evaluation events to Amazon SQS.
type Exporter struct {
	sqsService SQSSendMessageAPI
	// AwsConfig is the AWS SDK configuration object we will use to
	// upload your exported data files.
	AwsConfig *aws.Config
	// QueueURL is the URL of your SQS queue
	// (mandatory)
	QueueURL string
	init     sync.Once
}

// Export is sending SQS event for each events received.
func (f *Exporter) Export(
	ctx context.Context,
	_ *fflog.FFLogger,
	events []exporter.ExportableEvent,
) error {
	if f.AwsConfig == nil {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return fmt.Errorf("impossible to init SQS exporter: %w", err)
		}

		f.AwsConfig = &cfg
	}

	if f.QueueURL == "" {
		return errImpossibleToInitSQS
	}

	if f.sqsService == nil {
		f.init.Do(func() {
			f.sqsService = sqs.NewFromConfig(*f.AwsConfig)
		})
	}

	for _, event := range events {
		messageBody, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshal SQS event: %w", err)
		}

		_, err = f.sqsService.SendMessage(ctx, &sqs.SendMessageInput{
			MessageBody: aws.String(string(messageBody)),
			QueueUrl:    aws.String(f.QueueURL),
			MessageAttributes: map[string]types.MessageAttributeValue{
				"emitter": {
					DataType:    aws.String("String"),
					StringValue: aws.String("GO Feature Flag"),
				},
			},
		})
		if err != nil {
			return fmt.Errorf("send SQS message: %w", err)
		}
	}

	return nil
}

// IsBulk reports whether the exporter sends events in bulk.
func (f *Exporter) IsBulk() bool {
	return false
}

// SQSSendMessageAPI defines the interface for the GetQueueUrl and SendMessage functions.
// We use this interface to test the functions using a mocked service.
type SQSSendMessageAPI interface {
	SendMessage(ctx context.Context,
		params *sqs.SendMessageInput,
		optFns ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}
