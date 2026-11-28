package events

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// ErrExporterPanicked is matched by the error that stands for the panic of an exporter. The engine manager treats
// it like any failed flush.
var ErrExporterPanicked = errors.New("events: the exporter panicked")

// minScrubLength is the length under which a secret is not looked for in the messages of errors: a short value
// would hide ordinary text. Such a secret is still never logged by this package.
const minScrubLength = 8

// classify gives the class of an error. The message is never kept: it could carry a secret.
func classify(err error) ErrorClass {
	switch {
	case err == nil:
		return ErrorNone
	case errors.Is(err, ErrExporterPanicked):
		return ErrorPanic
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

// nameStats are the cumulative counters of one exporter name. They live as long as the dispatcher: a reload that
// replaces the instance of the name does not reset them.
type nameStats struct {
	last     lastError
	handed   atomic.Uint64
	exported atomic.Uint64
	handoff  atomic.Uint64
	drain    atomic.Uint64
	failures atomic.Uint64
	// blocked and exhausted count the events dropped by the block deadline and by the spent retry.
	blocked    atomic.Uint64
	exhausted  atomic.Uint64
	flushes    atomic.Uint64
	flushNanos atomic.Int64
	// mu guards the fold of the drops of a finished manager into mgrDropped, against a reader that adds the
	// drops of the managers still running.
	mu         sync.Mutex
	mgrDropped uint64
}

// instanceStats are the counters of one instance of an exporter: what it needs to tell, when it closes, which
// events were not delivered.
type instanceStats struct {
	handed   atomic.Uint64
	exported atomic.Uint64
	handoff  atomic.Uint64
	// blocked and exhausted are the drops of the block deadline and of the spent retry.
	blocked   atomic.Uint64
	exhausted atomic.Uint64
}

// countingExporter wraps an exporter to count what it exports, to hide its secrets from its errors, and to keep a
// panic of it from killing the relay. It is an [exporter.Exporter], the interface every engine exporter implements.
type countingExporter struct {
	inner    exporter.Exporter
	instance *instanceStats
	// name, pipe, after and rnd are set when the instance is launched.
	name *nameStats
	// after is the timer of the retry waits and rnd the source of their jitter, both injected.
	after func(time.Duration) <-chan time.Time
	rnd   func() float64
	// halt ends the retries when the exporter is closing and its drain deadline passed.
	halt atomic.Pointer[context.Context]
	// secrets are the forms of the secret values the errors of the exporter must not show.
	secrets     []string
	pipe        Pipeline
	panicLogged atomic.Bool
}

// IsBulk implements [exporter.CommonExporter].
func (c *countingExporter) IsBulk() bool { return c.inner.IsBulk() }

// String implements [fmt.Stringer]: the wrapper holds an exporter and secrets, it prints neither.
func (*countingExporter) String() string { return bootstrap.RedactedValue }

// GoString implements [fmt.GoStringer].
func (*countingExporter) GoString() string { return bootstrap.RedactedValue }

// Format implements [fmt.Formatter].
func (*countingExporter) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(bootstrap.RedactedValue)) }

// Export implements [exporter.Exporter]: it cuts the events into batches of the byte limit and exports each one,
// retrying a failure.
func (c *countingExporter) Export(ctx context.Context, logger *slog.Logger, events []exporter.ExportableEvent) error {
	for _, chunk := range c.chunks(events) {
		err := c.exportChunk(ctx, logger, chunk)
		if err != nil {
			return err
		}
	}

	return nil
}

// attempt makes one call of the exporter and counts it.
func (c *countingExporter) attempt(ctx context.Context, logger *slog.Logger, events []exporter.ExportableEvent) error {
	start := clock.Now()
	err := c.call(ctx, logger, events)
	elapsed := clock.Now().Sub(start)

	stats := c.name
	stats.flushes.Add(1)
	stats.flushNanos.Add(elapsed.Nanoseconds())
	stats.last.set(classify(err))

	if err != nil {
		stats.failures.Add(1)

		return c.scrub(err)
	}

	count := uint64(len(events))
	c.instance.exported.Add(count)
	stats.exported.Add(count)

	return nil
}

// call runs the exporter and turns a panic into an error: the engine manager runs the flush in a goroutine of
// its own, where a panic would end the process.
func (c *countingExporter) call(
	ctx context.Context,
	logger *slog.Logger,
	events []exporter.ExportableEvent,
) (err error) {
	defer func() {
		rec := recover()
		if rec == nil {
			return
		}

		// the value of a panic can carry a secret: only its type is kept
		if logger != nil && c.panicLogged.CompareAndSwap(false, true) {
			logger.LogAttrs(ctx, slog.LevelError, "the exporter panicked: the flush is treated as failed",
				slog.String("type", fmt.Sprintf("%T", rec)))
		}

		err = fmt.Errorf("%w: %T", ErrExporterPanicked, rec)
	}()

	return c.inner.Export(ctx, logger, events) //nolint:wrapcheck // transparent: scrub and the manager report it
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

	for _, form := range c.secrets {
		scrubbed = strings.ReplaceAll(scrubbed, form, bootstrap.RedactedValue)
	}

	if scrubbed == msg {
		return err
	}

	return &redactedError{err: err, msg: scrubbed}
}

// scrubForms returns the forms a secret can take in an error message: raw, escaped for JSON, a query or a path,
// and base64 (both alphabets, padded or not). Secrets shorter than [minScrubLength] give none. The longest forms
// come first, so that a form that contains another is replaced whole.
func scrubForms(secrets []string) []string {
	seen := map[string]struct{}{}

	var forms []string

	add := func(form string) {
		if len(form) < minScrubLength {
			return
		}

		if _, dup := seen[form]; !dup {
			seen[form] = struct{}{}
			forms = append(forms, form)
		}
	}

	for _, secret := range secrets {
		if len(secret) < minScrubLength {
			continue
		}

		add(secret)
		add(url.QueryEscape(secret))
		add(url.PathEscape(secret))

		for _, escapeHTML := range []bool{true, false} {
			add(jsonEscaped(secret, escapeHTML))
		}

		raw := []byte(secret)
		add(base64.StdEncoding.EncodeToString(raw))
		add(base64.RawStdEncoding.EncodeToString(raw))
		add(base64.URLEncoding.EncodeToString(raw))
		add(base64.RawURLEncoding.EncodeToString(raw))
	}

	slices.SortStableFunc(forms, func(a, b string) int { return len(b) - len(a) })

	return forms
}

// jsonEscaped returns the content of the JSON string that encodes secret.
func jsonEscaped(secret string, escapeHTML bool) string {
	var out strings.Builder

	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(escapeHTML)

	err := enc.Encode(secret)
	if err != nil {
		return ""
	}

	encoded := strings.TrimSuffix(out.String(), "\n")

	return encoded[1 : len(encoded)-1]
}
