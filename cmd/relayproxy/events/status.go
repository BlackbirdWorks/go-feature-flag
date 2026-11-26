package events

// Reasons for which an event or a change is dropped. The set is closed: it is the label set of the metrics.
const (
	// ReasonQueueFull: the event channel (or the queue of a notifier) was full.
	ReasonQueueFull = "queue_full"
	// ReasonShutdown: the component was shutting down or shut down.
	ReasonShutdown = "shutdown"
	// ReasonNoExporter: a worker took the event while the set of exporters was empty (a reload to no exporter).
	ReasonNoExporter = "no_exporter"
	// ReasonOverflow: the queue of an exporter overflowed (its manager dropped the event).
	ReasonOverflow = "overflow"
	// ReasonDrain: the event was not delivered when its exporter closed: the drain deadline passed or the final
	// flush failed.
	ReasonDrain = "drain"
)

// Component states.
const (
	// StateRunning: the exporter or notifier takes events.
	StateRunning = "running"
	// StateDraining: it was replaced or removed and is delivering what it holds.
	StateDraining = "draining"
	// StateStopped: it drained and closed.
	StateStopped = "stopped"
	// StateCut: its drain passed the deadline and was cut.
	StateCut = "cut"
)

// ErrorClass is the closed class of the last error of an exporter or notifier. It never holds a message: a
// message could carry a secret.
type ErrorClass string

// Error classes.
const (
	ErrorNone     ErrorClass = "none"
	ErrorTimeout  ErrorClass = "timeout"
	ErrorCanceled ErrorClass = "canceled"
	ErrorPanic    ErrorClass = "panic"
	ErrorOther    ErrorClass = "other"
)

// Drops counts dropped events by reason.
type Drops struct {
	QueueFull  uint64 `json:"queueFull"`
	Shutdown   uint64 `json:"shutdown"`
	NoExporter uint64 `json:"noExporter"`
	Overflow   uint64 `json:"overflow"`
	Drain      uint64 `json:"drain"`
}

// Total is the sum of every reason.
func (d Drops) Total() uint64 { return d.QueueFull + d.Shutdown + d.NoExporter + d.Overflow + d.Drain }

// QueueStatus is the state of the event channel.
type QueueStatus struct {
	Dropped  Drops  `json:"dropped"`
	Accepted uint64 `json:"accepted"`
	Length   int    `json:"length"`
	Capacity int    `json:"capacity"`
}

// ExporterStatus is the state of one exporter. It holds no secret.
type ExporterStatus struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	// LastError is the class of the error of the last flush.
	LastError ErrorClass `json:"lastError"`
	Dropped   Drops      `json:"dropped"`
	// Queued is the number of events handed to the exporter that are neither exported nor dropped.
	Queued int64 `json:"queued"`
	// Handed is the number of events handed to the exporter.
	Handed uint64 `json:"handed"`
	// Exported is the number of events the exporter wrote.
	Exported uint64 `json:"exported"`
	// FlushFailures is the number of failed flushes.
	FlushFailures uint64 `json:"flushFailures"`
	// Flushes and FlushSeconds are the count and the total duration of the flushes.
	Flushes      uint64  `json:"flushes"`
	FlushSeconds float64 `json:"flushSeconds"`
}

// NotifierStatus is the state of one notifier. It holds no secret.
type NotifierStatus struct {
	Name      string     `json:"name"`
	Kind      string     `json:"kind"`
	State     string     `json:"state"`
	LastError ErrorClass `json:"lastError"`
	// Queued is the number of changes waiting in the queue of the notifier.
	Queued    int    `json:"queued"`
	Delivered uint64 `json:"delivered"`
	Errors    uint64 `json:"errors"`
	Panics    uint64 `json:"panics"`
	// Dropped counts the changes that did not fit in the queue or were cut at shutdown.
	Dropped uint64 `json:"dropped"`
}

// Status is an immutable snapshot of the exporters and the notifiers, read without blocking.
type Status struct {
	Exporters []ExporterStatus `json:"exporters"`
	Notifiers []NotifierStatus `json:"notifiers"`
	Queue     QueueStatus      `json:"queue"`
}

// StatusReader is implemented by [Dispatcher], [Notifications] and [Runtime].
type StatusReader interface {
	Status() Status
}
