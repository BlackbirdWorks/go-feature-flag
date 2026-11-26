package metric

import (
	"fmt"

	prom "github.com/prometheus/client_golang/prometheus"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// otherLabelValue is the label value of an operation or a result outside the closed sets of the connection
// package, so a new value there can never create a new series by itself.
const otherLabelValue = "other"

// S3Requests counts the requests of the S3 clients of the connection factory in
// semaphore_s3_requests_total{connection,operation,result}. It implements [connection.RequestObserver]: the
// factory calls it once per attempt of the SDK, so a request retried after a 503 is two observations and a
// 304 is one not_modified, never an error.
//
// The only labels are the name of the connection (fixed by the bootstrap file), the operation and the result
// (closed sets): there is no per-flag, per-object or per-key label. It is not part of [Metrics] and not
// registered by [NewMetrics]: the relay registers it on its registry when it uses connections (config v2), so
// the output of the running relay does not change before.
type S3Requests struct {
	counter *prom.CounterVec
}

var _ connection.RequestObserver = (*S3Requests)(nil)

// NewS3Requests creates the counter and registers it on reg.
func NewS3Requests(reg prom.Registerer) (*S3Requests, error) {
	counter := prom.NewCounterVec(prom.CounterOpts{
		Namespace: SubSystem,
		Name:      "s3_requests_total",
		Help: "S3 requests by connection, operation (get, list, head, put, delete, other) and result (ok, " +
			"not_modified, not_found, throttled, client_error, redirect, server_error, error); " +
			"one count per attempt of the SDK.",
	}, []string{"connection", "operation", "result"})

	err := reg.Register(counter)
	if err != nil {
		return nil, fmt.Errorf("register s3 requests metric: %w", err)
	}

	return &S3Requests{counter: counter}, nil
}

// Observe implements [connection.RequestObserver].
func (s *S3Requests) Observe(conn, operation, result string) {
	s.counter.WithLabelValues(conn, operationLabel(operation), resultLabel(result)).Inc()
}

func operationLabel(value string) string {
	switch value {
	case connection.OperationGet, connection.OperationList, connection.OperationHead,
		connection.OperationPut, connection.OperationDelete, connection.OperationOther:
		return value
	}

	return otherLabelValue
}

func resultLabel(value string) string {
	switch value {
	case connection.ResultOK, connection.ResultNotModified, connection.ResultNotFound, connection.ResultThrottled,
		connection.ResultClientError, connection.ResultRedirect, connection.ResultServerError, connection.ResultError:
		return value
	}

	return otherLabelValue
}
