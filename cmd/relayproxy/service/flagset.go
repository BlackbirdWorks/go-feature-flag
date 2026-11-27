package service

import (
	"context"
	"time"

	"github.com/blackbirdworks/semaphore/exporter"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/model"
)

// FlagSet is what the HTTP handlers use of one flag set: the evaluation, the flag configuration, the collection of
// events and the refresh. The v1 client (`*ffclient.GoFeatureFlag`) and the config v2 runtime satisfy it.
// A value that an evaluation returns is shared: the handlers only read it, and never change it.
type FlagSet interface {
	// RawVariation evaluates one flag without a type.
	RawVariation(flagKey string, evalCtx ffcontext.Context, defaultValue any) (model.RawVarResult, error)
	// GetFlagStates evaluates the listed flags.
	GetFlagStates(evalCtx ffcontext.Context, flagsToEvaluate []string) flagstate.AllFlags
	// AllFlagsState evaluates every flag.
	AllFlagsState(evalCtx ffcontext.Context) flagstate.AllFlags
	// GetFlagsFromCache returns the flags of the flag set.
	GetFlagsFromCache() (map[string]flagdef.Flag, error)
	// GetFlagsFromCacheWithContext is GetFlagsFromCache with the context of the request.
	GetFlagsFromCacheWithContext(ctx context.Context) (map[string]flagdef.Flag, error)
	// GetCacheRefreshDate is the date the flags were last refreshed.
	GetCacheRefreshDate() time.Time
	// GetEvaluationContextEnrichment is the enrichment that every evaluation of the flag set gets.
	GetEvaluationContextEnrichment() map[string]any
	// CollectEventData hands a feature event to the exporters.
	CollectEventData(event *exporter.FeatureEvent)
	// CollectTrackingEventData hands a tracking event to the exporters.
	CollectTrackingEventData(event *exporter.TrackingEvent)
	// ForceRefresh asks the flag source for the latest flags and reports whether it did.
	ForceRefresh() bool
}

// FlagSetResolver finds the flag set of an API key. It is the seam of the HTTP layer: the v1 flag set manager and the
// config v2 core both implement it.
type FlagSetResolver interface {
	// FlagSet returns the flag set linked to the API Key
	FlagSet(apiKey string) (FlagSet, error)
	// FlagSetName returns the name of the flagset linked to the API Key.
	// In default mode it returns the default flagset name, with flagsets it returns an
	// error if the API Key is empty or not linked to a flagset.
	FlagSetName(apiKey string) (string, error)
	// IsDefaultFlagSet returns true if the manager is in default mode (no flagsets configured)
	IsDefaultFlagSet() bool
}

// EventAcceptor is a [FlagSet] that tells whether it accepted a feature event: its queue to the exporters is bounded
// and a full queue drops the event. The collector then counts only the accepted events as ingested. A flag set that
// does not implement it (the v1 client) accepts every event.
type EventAcceptor interface {
	// AcceptEventData hands a feature event to the exporters and reports whether it was accepted.
	AcceptEventData(event *exporter.FeatureEvent) bool
}

// FlagChanger is a [FlagSet] that keeps the hashes of its flags: the flag change endpoint answers with them instead
// of encoding every flag again. The returned map is shared: the caller only reads it.
type FlagChanger interface {
	// FlagChange returns the hash of every flag and the hash of the whole table.
	FlagChange() (flags map[string]uint32, hash uint32, err error)
}

// RefreshUnsupported is a [FlagSetResolver] whose flag sets can not be refreshed on request: the refresh endpoint
// answers 501 with the reason.
type RefreshUnsupported interface {
	// RefreshUnsupported is the reason, sent to the client.
	RefreshUnsupported() string
}
