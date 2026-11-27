package bootstrap

// The paths of the shutdown settings in the messages of the loader.
const (
	pathDrainDelay = "server.shutdown.drainDelay"
	pathTimeout    = "server.shutdown.timeout"
)

// Shutdown is how the relay stops: it fails the readiness, waits the drain delay while it keeps serving, then stops
// the listeners and the core within the timeout.
type Shutdown struct {
	// DrainDelay is how long the relay keeps serving after the readiness answered 503, so that load balancers stop
	// sending traffic. Unset means no delay.
	DrainDelay *Duration `json:"drainDelay,omitempty" yaml:"drainDelay,omitempty"`
	// Timeout bounds the stop that follows the drain delay (default 15s). When it ends the relay stops hard.
	Timeout *Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}
