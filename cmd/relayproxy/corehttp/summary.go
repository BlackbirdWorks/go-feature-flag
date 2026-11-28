package corehttp

import (
	"slices"
	"sync/atomic"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
)

// The states of a resource in /info and in the metrics. A flag set that serves without a problem is "ready".
const (
	resourceReady    = "ready"
	resourceDegraded = "degraded"
	resourcePending  = "pending"
)

// Resource kinds of the resource metrics.
const (
	kindFlagSet = "flagset"
	kindObject  = "object"
	// kindDynamicSegment counts the dynamic segment definitions of the flag sets, by state.
	kindDynamicSegment = "dynamicSegment"
	// The states of a dynamic segment: served as defined, or rejected because the connections cannot serve it.
	stateValid    = "valid"
	stateRejected = "rejected"
)

// unhealthy is a resource that is not ready.
type unhealthy struct {
	name  string
	state string
}

// summary is what the probes and the metrics need of one status tree. It is computed once per runtime, when the
// first reader meets it, never per scrape or per request.
type summary struct {
	// oldestNotOK is when the oldest object that is not ok left that state; zero when every object is ok.
	oldestNotOK time.Time
	flagSets    map[string]int
	objects     map[string]int
	// rejected counts the rejected flags present in the objects, by reason (kept, dropped).
	rejected  map[string]int
	dynamic   map[string]int
	badSets   []unhealthy
	badObject []unhealthy
}

// summaries caches the summary of the runtime that was last read. A new runtime is a new pointer, so the cache
// needs no invalidation.
type summaries struct {
	last atomic.Pointer[cachedSummary]
	// computed counts the summaries made (tests pin that a scrape does not make one).
	computed atomic.Uint64
}

type cachedSummary struct {
	status *flagruntime.Status
	sum    *summary
}

// addObjects counts the objects by state and the rejected flags, and notes the objects that are not ok.
func (sum *summary) addObjects(objects map[string]*flagruntime.ObjectStatus) {
	for name, obj := range objects {
		state := string(obj.State)
		sum.objects[state]++

		for _, rej := range obj.Rejected {
			if rej.Kept {
				sum.rejected[relaycore.RejectedKept]++
			} else {
				sum.rejected[relaycore.RejectedDropped]++
			}
		}

		if obj.State != flagruntime.ObjectOK {
			if !obj.Since.IsZero() && (sum.oldestNotOK.IsZero() || obj.Since.Before(sum.oldestNotOK)) {
				sum.oldestNotOK = obj.Since
			}

			sum.badObject = append(sum.badObject, unhealthy{name: name, state: state})
		}
	}
}

func (c *summaries) of(status *flagruntime.Status) *summary {
	if status == nil {
		return &summary{
			flagSets: map[string]int{},
			objects:  map[string]int{},
			rejected: map[string]int{},
			dynamic:  map[string]int{},
		}
	}

	if cached := c.last.Load(); cached != nil && cached.status == status {
		return cached.sum
	}

	c.computed.Add(1)

	sum := &summary{
		flagSets: map[string]int{},
		objects:  map[string]int{},
		rejected: map[string]int{},
		dynamic:  map[string]int{},
	}

	for name, set := range status.FlagSets {
		state := setLabel(set.State)
		sum.flagSets[state]++

		if set.Segments != nil {
			sum.dynamic[stateValid] += set.Segments.Dynamic - set.Segments.RejectedDefinitions
			sum.dynamic[stateRejected] += set.Segments.RejectedDefinitions
		}

		if state != resourceReady {
			sum.badSets = append(sum.badSets, unhealthy{name: name, state: state})
		}
	}

	sum.addObjects(status.Objects)

	byName := func(a, b unhealthy) int {
		if a.name < b.name {
			return -1
		}

		if a.name > b.name {
			return 1
		}

		return 0
	}
	slices.SortFunc(sum.badSets, byName)
	slices.SortFunc(sum.badObject, byName)

	c.last.Store(&cachedSummary{status: status, sum: sum})

	return sum
}

// setLabel maps the state of a flag set of the runtime to the state of #5: serving is ready.
func setLabel(state flagruntime.SetState) string {
	switch state {
	case flagruntime.StateServing:
		return resourceReady
	case flagruntime.StatePending:
		return resourcePending
	case flagruntime.StateDegraded, flagruntime.StateWithdrawn:
	}

	return resourceDegraded
}
