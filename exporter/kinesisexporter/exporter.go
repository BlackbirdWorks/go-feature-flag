package kinesisexporter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/kinesis/types"

	"github.com/blackbirdworks/semaphore/exporter"
)

var (
	errSendNoStreamNameOr = errors.New("send: no StreamName or StreamArn provided")
	errSend               = errors.New("send")
)

var _ exporter.Exporter = &Exporter{}

const (
	// Mb is the number of bytes in a megabyte.
	//nolint:varnamelen // exported constant kept for compatibility
	Mb = 1024 * 1024

	// Kinesis PutRecords limits: 500 records and 5 MiB per call.
	maxRecordsPerCall = 500
	maxBytesPerCall   = 5 * Mb

	maxAttempts = 3
	baseBackoff = 100 * time.Millisecond
	maxBackoff  = 2 * time.Second
)

// DefaultPartitionKey is the partition key function used when none is configured.
func DefaultPartitionKey(ctx context.Context, _ exporter.ExportableEvent) string {
	ctx.Value("feature")

	return "default"
}

// MessageSender sends encoded messages to Kinesis.
type MessageSender interface {
	SendMessages(
		ctx context.Context,
		msgs *kinesis.PutRecordsInput,
	) (*kinesis.PutRecordsOutput, error)
}

// DefaultKinesisSender sends messages to Kinesis with the AWS SDK.
type DefaultKinesisSender struct {
	*kinesis.Client
}

// SendMessages sends the messages to Kinesis.
func (k *DefaultKinesisSender) SendMessages(
	ctx context.Context,
	msgs *kinesis.PutRecordsInput,
) (*kinesis.PutRecordsOutput, error) {
	out, err := k.PutRecords(ctx, msgs)
	if err != nil {
		return nil, fmt.Errorf("put kinesis records: %w", err)
	}

	return out, nil
}

// Exporter sends evaluation events to Amazon Kinesis.
type Exporter struct {
	sender MessageSender
	// loadConfig loads the configuration of the default AWS chain when AwsConfig is nil. It is nil in production (then
	// config.LoadDefaultConfig) and is replaced only by tests: an exporter that is given its configuration, as the
	// relay proxy does from the connection factory, never calls it.
	loadConfig func(context.Context) (aws.Config, error)
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
	// initMu guards the initialisation, which is retried by the next call until it has succeeded.
	initMu   sync.Mutex
	initDone bool
}

// PartitionKeyFunc returns the partition key of an evaluation event.
type PartitionKeyFunc = func(context.Context, exporter.ExportableEvent) string

// Settings holds the configuration of the Kinesis exporter.
type Settings struct {
	StreamName      *string
	StreamArn       *string
	PartitionKey    PartitionKeyFunc
	ExplicitHashKey *string
}

// Option changes the settings of the Kinesis exporter.
type Option = func(Settings) Settings

// WithPartitionKey sets the function that computes the partition key of an event.
func WithPartitionKey(partitionKey PartitionKeyFunc) Option {
	return func(s Settings) Settings {
		s.PartitionKey = partitionKey

		return s
	}
}

// WithExplicitHashKey sets the explicit hash key of the records.
func WithExplicitHashKey(hashKey string) Option {
	return func(s Settings) Settings {
		s.ExplicitHashKey = &hashKey

		return s
	}
}

// WithStreamName sets the name of the Kinesis stream.
func WithStreamName(streamName string) Option {
	return func(settings Settings) Settings {
		if streamName == "" {
			return settings
		}

		settings.StreamArn = nil
		settings.StreamName = &streamName

		return settings
	}
}

// WithStreamArn sets the ARN of the Kinesis stream.
func WithStreamArn(streamArn string) Option {
	return func(settings Settings) Settings {
		if streamArn == "" {
			return settings
		}

		settings.StreamArn = &streamArn
		settings.StreamName = nil

		return settings
	}
}

// NewSettings returns the Kinesis exporter settings with the options applied.
func NewSettings(options ...Option) *Settings {
	settings := Settings{
		StreamName:      nil,
		StreamArn:       nil,
		PartitionKey:    DefaultPartitionKey,
		ExplicitHashKey: nil,
	}
	for _, option := range options {
		settings = option(settings)
	}

	return &settings
}

// Export sends the events to Kinesis.
func (e *Exporter) Export(
	ctx context.Context,
	logger *slog.Logger,
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
			logger.ErrorContext(ctx, "format: Event is too large, skipping", slog.Int("size", len(formattedEvent)))

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

		sendChunkErr := e.sendChunk(ctx, logger, &chunkInput)
		if sendChunkErr != nil {
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
		recSize := len(r.Data) + len(aws.ToString(r.PartitionKey))
		if len(current) > 0 && (len(current) >= maxRecordsPerCall || size+recSize > maxBytesPerCall) {
			chunks = append(chunks, current)
			current, size = newChunk(), 0
		}

		current = append(current, r)
		size += recSize
	}

	if len(current) > 0 {
		chunks = append(chunks, current)
	}

	return chunks
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

	var (
		retry    []types.PutRecordsRequestEntry
		failures []types.PutRecordsResultEntry
	)

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

	slices.Sort(codes)

	return strings.Join(codes, ", ")
}

// backoff returns an exponential delay with full jitter for the given attempt (1-based).
func backoff(attempt int) time.Duration {
	d := min(baseBackoff<<(attempt-1), maxBackoff)

	return rand.N(d) + 1 //nolint:gosec // jitter does not need a CSPRNG
}

// IsBulk returns false: this exporter sends each event on its own as a Kinesis record, it does not batch events.
func (*Exporter) IsBulk() bool {
	return false
}

func (e *Exporter) initializeProducer(ctx context.Context) error {
	e.initMu.Lock()
	defer e.initMu.Unlock()

	if e.initDone {
		return nil
	}

	if e.Settings == nil {
		e.Settings = NewSettings()
	}

	if e.AwsConfig == nil {
		cfg, err := e.defaultConfig(ctx)
		if err != nil {
			return fmt.Errorf("impossible to init Kinesis exporter: %w", err)
		}

		e.AwsConfig = &cfg
	}

	if e.sender == nil {
		e.sender = &DefaultKinesisSender{
			kinesis.NewFromConfig(*e.AwsConfig, e.KinesisOptions...),
		}
	}

	e.initDone = true

	return nil
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
	logger *slog.Logger,
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

		logger.WarnContext(ctx, "send: some records were rejected by Kinesis",
			slog.Int("failed", len(retry)), slog.Int("total", total), slog.Int("attempt", attempt))

		if attempt >= maxAttempts {
			return fmt.Errorf("%w: %d of %d records failed after %d attempts (error codes: %s)", errSend,
				len(retry), total, attempt, distinctCodes(failures))
		}

		pending = retry

		waitErr := e.wait(ctx, backoff(attempt))
		if waitErr != nil {
			return fmt.Errorf("send: %d of %d records failed, retry interrupted: %w",
				len(retry), total, waitErr)
		}
	}
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
		return fmt.Errorf("waiting to retry the Kinesis export: %w", ctx.Err())
	case <-t.C:
		return nil
	}
}

// formatMessage returns the event encoded in the selected format. Will always use JSON for now.
func (*Exporter) formatMessage(event exporter.ExportableEvent) ([]byte, error) {
	b, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal kinesis event: %w", err)
	}

	return b, nil
}

// defaultConfig loads the configuration of the default AWS chain; it is only reached by an exporter that is used as
// a library and has no AwsConfig.
func (e *Exporter) defaultConfig(ctx context.Context) (aws.Config, error) {
	if e.loadConfig != nil {
		return e.loadConfig(ctx)
	}

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load the default AWS configuration: %w", err)
	}

	return cfg, nil
}
