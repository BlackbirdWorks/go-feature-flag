package model

// LiveResponse is the body of /livez.
type LiveResponse struct {
	// Live is true while the process serves.
	Live bool `example:"true" json:"live"`
}

// ReadyResponse is the body of /readyz and /startupz. It never holds a key, a name or a credential.
type ReadyResponse struct {
	// Ready is true when the relay is able to serve.
	Ready bool `example:"true" json:"ready"`
	// State is set when ready: empty, serving or degraded.
	State string `example:"serving" json:"state,omitempty"`
	// Reason is set when not ready: starting, waiting-for-manifest or shutting-down.
	Reason string `example:"waiting-for-manifest" json:"reason,omitempty"`
}
