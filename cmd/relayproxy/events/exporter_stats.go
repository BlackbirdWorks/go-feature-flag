package events

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// classify gives the class of an error. The message is never kept: it could carry a secret.
func classify(err error) ErrorClass {
	switch {
	case err == nil:
		return ErrorNone
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorTimeout
	case errors.Is(err, context.Canceled):
		return ErrorCanceled
	default:
		return ErrorOther
	}
}

const (
	classNone int32 = iota
	classTimeout
	classCanceled
	classPanic
	classOther
)

// classIndex and classOf map the closed set of classes to a number that fits an atomic.
func classIndex(class ErrorClass) int32 {
	switch class {
	case ErrorNone:
		return classNone
	case ErrorTimeout:
		return classTimeout
	case ErrorCanceled:
		return classCanceled
	case ErrorPanic:
		return classPanic
	case ErrorOther:
		return classOther
	}

	return classOther
}

func classOf(index int32) ErrorClass {
	switch index {
	case classNone:
		return ErrorNone
	case classTimeout:
		return ErrorTimeout
	case classCanceled:
		return ErrorCanceled
	case classPanic:
		return ErrorPanic
	}

	return ErrorOther
}

// lastError is an atomically readable [ErrorClass].
type lastError struct{ v atomic.Int32 }

func (l *lastError) set(class ErrorClass) { l.v.Store(classIndex(class)) }

func (l *lastError) get() ErrorClass { return classOf(l.v.Load()) }

// exporterStats are the counters of the flushes of one exporter.
type exporterStats struct {
	last       lastError
	exported   atomic.Uint64
	failures   atomic.Uint64
	flushes    atomic.Uint64
	flushNanos atomic.Int64
}

// countingExporter wraps an exporter to count what it exports. It is an [exporter.Exporter], the interface every
// engine exporter implements.
type countingExporter struct {
	inner exporter.Exporter
	stats *exporterStats
	// secrets are the values the errors of the exporter must not show: an HTTP client error carries the URL.
	secrets []string
}

// IsBulk implements [exporter.CommonExporter].
func (c *countingExporter) IsBulk() bool { return c.inner.IsBulk() }

// Export implements [exporter.Exporter].
func (c *countingExporter) Export(ctx context.Context, logger *slog.Logger, events []exporter.ExportableEvent) error {
	start := clock.Now()
	err := c.inner.Export(ctx, logger, events)
	elapsed := clock.Now().Sub(start)

	c.stats.flushes.Add(1)
	c.stats.flushNanos.Add(int64(elapsed / time.Nanosecond))
	c.stats.last.set(classify(err))

	if err != nil {
		c.stats.failures.Add(1)

		return c.scrub(err)
	}

	c.stats.exported.Add(uint64(len(events)))

	return nil
}

// redactedError is an error whose message hides the secrets of the exporter. It still unwraps to the cause, so
// [errors.Is] and [errors.As] keep working.
type redactedError struct {
	err error
	msg string
}

func (e *redactedError) Error() string { return e.msg }

func (e *redactedError) Unwrap() error { return e.err }

// scrub hides the secrets of the exporter in the message of err.
func (c *countingExporter) scrub(err error) error {
	if len(c.secrets) == 0 {
		return err
	}

	msg := err.Error()
	scrubbed := msg

	for _, secret := range c.secrets {
		scrubbed = strings.ReplaceAll(scrubbed, secret, bootstrap.RedactedValue)
		scrubbed = strings.ReplaceAll(scrubbed, url.QueryEscape(secret), bootstrap.RedactedValue)
		scrubbed = strings.ReplaceAll(scrubbed, url.PathEscape(secret), bootstrap.RedactedValue)
	}

	if scrubbed == msg {
		return err
	}

	return &redactedError{err: err, msg: scrubbed}
}
