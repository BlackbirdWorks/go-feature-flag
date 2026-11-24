package kinesisexporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errSendNoStreamNameOr = errors.New("send: no StreamName or StreamArn provided")
	errSend               = errors.New("send")
)

var _ exporter.Exporter = &Exporter{}

const (
	formatJSON = "json"
	Mb         = 1024 * 1024

	// Kinesis PutRecords limits: 500 records and 5 MiB per call.
	maxRecordsPerCall = 500
	maxBytesPerCall   = 5 * Mb

	maxAttempts = 3
	baseBackoff = 100 * time.Millisecond
	maxBackoff  = 2 * time.Second
)

// DefaultPartitionKey is the partition key function used when none is configured.
func DefaultPartitionKey(context context.Context, _ exporter.ExportableEvent) string {
	context.Value("feature")

	return "default"
}

type MessageSender interface {
	SendMessages(
		ctx context.Context,
		msgs *kinesis.PutRecordsInput,
	) (*kinesis.PutRecordsOutput, error)
}

type DefaultKinesisSender struct {
	*kinesis.Client
}

func (k *DefaultKinesisSender) SendMessages(
	ctx context.Context,
	msgs *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	return k.PutRecords(ctx, msgs)
}

type Exporter struct {
	sender MessageSender
	// AwsConfig is the AWS SDK configuration object we will use to
	// upload your exported data files.
	AwsConfig *aws.Config
	Settings  *Settings
	// sleep replaces the real backoff wait, it is only set by tests.
	sleep func(ctx context.Context, d time.Duration) error
	// Format is the output format you want in your exported file.
	// Available format are JSON, CSV and Parquet.
	// Default: JSON
	Format string
	// kinesis.Options is a list of functional options to configure the Kinesis client.
	// Provide additional functional options to further configure the behavior of the client,
	// such as changing the client's endpoint or adding custom middleware behavior.
	// For more information about the options, please check:
	// https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/kinesis#Options
	KinesisOptions []func(*kinesis.Options)
	init           sync.Once
}

type PartitionKeyFunc = func(context.Context, exporter.ExportableEvent) string

type Settings struct {
	StreamName      *string
	StreamArn       *string
	PartitionKey    PartitionKeyFunc
	ExplicitHashKey *string
}

type Option = func(Settings) Settings

func WithPartitionKey(partitionKey PartitionKeyFunc) Option {
	return func(s Settings) Settings {
		s.PartitionKey = partitionKey

		return s
	}
}

func WithExplicitHashKey(hashKey string) Option {
	return func(s Settings) Settings {
		s.ExplicitHashKey = &hashKey

		return s
	}
}

func WithStreamName(streamName string) Option {
	return func(s Settings) Settings {
		if streamName == "" {
			return s
		}
		s.StreamArn = nil
		s.StreamName = &streamName

		return s
	}
}

func WithStreamArn(streamArn string) Option {
	return func(s Settings) Settings {
		if streamArn == "" {
			return s
		}
		s.StreamArn = &streamArn
		s.StreamName = nil

		return s
	}
}

func NewSettings(options ...Option) *Settings {
	settings := Settings{
		PartitionKey: DefaultPartitionKey,
	}
	for _, option := range options {
		settings = option(settings)
	}

	return &settings
}

func (e *Exporter) initializeProducer(ctx context.Context) error {
	var initErr error
	e.init.Do(func() {
		if e.Settings == nil {
			e.Settings = NewSettings()
		}

		if e.AwsConfig == nil {
			cfg, err := config.LoadDefaultConfig(ctx)
			if err != nil {
				initErr = fmt.Errorf("impossible to init Kinesis exporter: %w", err)

				return
			}
			e.AwsConfig = &cfg
		}
		if e.sender == nil {
			e.sender = &DefaultKinesisSender{
				kinesis.NewFromConfig(*e.AwsConfig, e.KinesisOptions...),
			}
		}
	})

	return initErr
}

func (e *Exporter) Export(
	ctx context.Context,
	logger *fflog.FFLogger,
	featureEvents []exporter.ExportableEvent,
) error {
	err := e.initializeProducer(ctx)
	if err != nil {
		return fmt.Errorf("writer: %w", err)
	}

	records := make([]types.PutRecordsRequestEntry, 0, len(featureEvents))

	for _, event := range featureEvents {
		formattedEvent, formatMessageErr := e.formatMessage(event)

		if formatMessageErr != nil {
			return fmt.Errorf("format: %w", formatMessageErr)
		}

		if len(formattedEvent) >= Mb {
			logger.Error("format: Event is too large, skipping", "size", len(formattedEvent))

			continue
		}

		partitionKey := e.Settings.PartitionKey(ctx, event)

		records = append(records, types.PutRecordsRequestEntry{
			Data:            formattedEvent,
			PartitionKey:    &partitionKey,
			ExplicitHashKey: e.Settings.ExplicitHashKey,
		})
	}

	input := &kinesis.PutRecordsInput{}

	switch {
	case e.Settings.StreamArn != nil:
		input.StreamARN = e.Settings.StreamArn
	case e.Settings.StreamName != nil:
		input.StreamName = e.Settings.StreamName
	default:
		return errSendNoStreamNameOr
	}

	if len(records) == 0 {
		// nothing to send
		return nil
	}

	var errs []error
	for _, chunk := range chunkRecords(records) {
		chunkInput := *input
		chunkInput.Records = chunk
		if sendChunkErr := e.sendChunk(ctx, logger, &chunkInput); sendChunkErr != nil {
			if ctx.Err() != nil {
				return sendChunkErr
			}
			errs = append(errs, sendChunkErr)
		}
	}

	return errors.Join(errs...)
}

// chunkRecords splits records into groups that respect the PutRecords limits
// (500 records and 5 MiB per call, partition keys included in the size).
func chunkRecords(records []types.PutRecordsRequestEntry) [][]types.PutRecordsRequestEntry {
	var chunks [][]types.PutRecordsRequestEntry
	newChunk := func() []types.PutRecordsRequestEntry {
		return make([]types.PutRecordsRequestEntry, 0, min(len(records), maxRecordsPerCall))
	}
	current := newChunk()
	size := 0
	for _, r := range records {
		rs := len(r.Data) + len(aws.ToString(r.PartitionKey))
		if len(current) > 0 && (len(current) >= maxRecordsPerCall || size+rs > maxBytesPerCall) {
			chunks = append(chunks, current)
			current, size = newChunk(), 0
		}
		current = append(current, r)
		size += rs
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}

	return chunks
}

// sendChunk sends one PutRecords call and retries only the records Kinesis rejected.
//
// PutRecords is not atomic: a response can accept some records and reject others
// (e.g. ProvisionedThroughputExceededException, InternalFailure). Records that
// were accepted are never resent within this call, so no duplicates are produced
// while retries succeed.
//
// Trade-off: Export(ctx, logger, events) error cannot report partial success. If
// records are still failing once the attempts are exhausted we return an error,
// and the caller (event store, ProcessPendingEvents) then keeps the whole batch
// and will resend it on the next flush, duplicating the records that were
// accepted here. We prefer that at-least-once behaviour over silently losing the
// rejected events; duplicates are limited to the retries-exhausted case.
func (e *Exporter) sendChunk(
	ctx context.Context,
	logger *fflog.FFLogger,
	input *kinesis.PutRecordsInput,
) error {
	pending := input.Records
	total := len(pending)

	for attempt := 1; ; attempt++ {
		req := *input
		req.Records = pending
		output, err := e.sender.SendMessages(ctx, &req)
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		retry, failures := rejectedRecords(pending, output)
		if len(retry) == 0 {
			return nil
		}
		logger.Warn("send: some records were rejected by Kinesis",
			"failed", len(retry), "total", total, "attempt", attempt)

		if attempt >= maxAttempts {
			return fmt.Errorf("%w: %d of %d records failed after %d attempts (error codes: %s)", errSend,
				len(retry), total, attempt, distinctCodes(failures))
		}
		pending = retry
		if waitErr := e.wait(ctx, backoff(attempt)); waitErr != nil {
			return fmt.Errorf("send: %d of %d records failed, retry interrupted: %w",
				len(retry), total, waitErr)
		}
	}
}

// rejectedRecords returns the records of pending that are not confirmed as delivered, together
// with the matching result entries (when available, for error reporting).
//
// A record is only considered delivered when its own positional result entry carries a
// SequenceNumber and no error. The aggregate FailedRecordCount is deliberately ignored: it is
// optional in the SDK and can contradict the per-record results. When the response cannot be
// matched to the request (nil output, or a different number of results) nothing can be
// confirmed, so every record is treated as failed and resent (at-least-once).
func rejectedRecords(
	pending []types.PutRecordsRequestEntry,
	output *kinesis.PutRecordsOutput,
) ([]types.PutRecordsRequestEntry, []types.PutRecordsResultEntry) {
	if output == nil || len(output.Records) != len(pending) {
		unknown := types.PutRecordsResultEntry{
			ErrorCode:    aws.String("UnverifiableResponse"),
			ErrorMessage: aws.String("response does not match the request"),
		}
		failures := make([]types.PutRecordsResultEntry, len(pending))
		for i := range failures {
			failures[i] = unknown
		}

		return pending, failures
	}

	var retry []types.PutRecordsRequestEntry
	var failures []types.PutRecordsResultEntry
	for i, res := range output.Records {
		if res.ErrorCode == nil && res.ErrorMessage == nil && res.SequenceNumber != nil {
			continue
		}
		retry = append(retry, pending[i])
		failures = append(failures, res)
	}

	return retry, failures
}

func distinctCodes(failures []types.PutRecordsResultEntry) string {
	set := map[string]struct{}{}
	for _, f := range failures {
		code := aws.ToString(f.ErrorCode)
		if code == "" {
			code = "Unknown"
		}
		set[code] = struct{}{}
	}
	codes := make([]string, 0, len(set))
	for c := range set {
		codes = append(codes, c)
	}
	sort.Strings(codes)

	return strings.Join(codes, ", ")
}

// backoff returns an exponential delay with full jitter for the given attempt (1-based).
func backoff(attempt int) time.Duration {
	d := min(baseBackoff<<(attempt-1), maxBackoff)

	return rand.N(d) + 1 //nolint:gosec // jitter does not need a CSPRNG
}

// wait blocks for d or until ctx is done. It uses the injectable sleep when set.
func (e *Exporter) wait(ctx context.Context, d time.Duration) error {
	if e.sleep != nil {
		return e.sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// formatMessage returns the event encoded in the selected format. Will always use JSON for now.
func (e *Exporter) formatMessage(event exporter.ExportableEvent) ([]byte, error) {
	switch e.Format {
	case formatJSON:
		fallthrough
	default:
		return json.Marshal(event)
	}
}

// IsBulk reports if the producer can handle bulk messages. Will always return false for this exporter.
func (e *Exporter) IsBulk() bool {
	return false
}
