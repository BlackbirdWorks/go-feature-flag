package corehttp

import (
	"slices"
	"strings"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// monitoring answers the health, the info and the probe endpoints from a core.
type monitoring struct {
	*Readiness

	holder *flagruntime.Holder
}

// Health reports that the server is initialized. It is liveness: it never fails.
func (monitoring) Health() model.HealthResponse { return model.HealthResponse{Initialized: true} }

// Info lists the flag sets with the date of their last change (the fields of v1) and the state of the runtime. It
// works with zero flag sets.
func (m monitoring) Info() (model.InfoResponse, error) {
	current := m.holder.Load()
	dates := make(map[string]time.Time, len(current.FlagSets))

	var latest time.Time

	for name, set := range current.FlagSets {
		dates[name] = set.ModifiedAt
		if set.ModifiedAt.After(latest) {
			latest = set.ModifiedAt
		}
	}

	status := m.src.Status()
	cfg := configInfo(&status, clock.Now())

	return model.InfoResponse{Flagsets: dates, LatestCacheRefresh: &latest, Config: &cfg}, nil
}

func configInfo(status *relaycore.Status, now time.Time) model.ConfigInfo {
	info := model.ConfigInfo{
		Reloads: map[string]uint64{
			string(flagruntime.OutcomeApplied):  status.Reloads.Applied,
			string(flagruntime.OutcomeRejected): status.Reloads.Rejected,
			string(flagruntime.OutcomeIgnored):  status.Reloads.Ignored,
		},
		FlagSets: []model.FlagSetInfo{}, Objects: []model.ObjectInfo{}, Withdrawn: []model.WithdrawnInfo{},
		PendingRestart: []string{},
		Public:         model.PublicInfo{Sets: []string{}, Flags: 0, Present: false},
		Staleness:      staleness(status, now),
		State:          stateInfo(&status.Source.Reader.State),
	}

	info.LastReload = roundInfo(status.Reloads.LastRound)
	info.LastApplied = roundInfo(status.Reloads.LastApplied)

	if status.Reloads.LastRejected != nil {
		rejected := roundInfo(status.Reloads.LastRejected)
		info.LastRejected = &rejected
	}

	current := status.Source.Runtime
	if current == nil {
		return info
	}

	info.Generation, info.Snapshot = current.Generation, current.Snapshot
	info.PendingRestart = append(info.PendingRestart, current.PendingRestart...)

	if public := current.Public; public != nil {
		info.Public = model.PublicInfo{Present: public.Present, Flags: public.Flags, Sets: slices.Clone(public.Sets)}
	}

	failures := map[string]manifestreader.ObjectFailure{}
	for _, failure := range status.Source.Reader.Failures {
		failures[failure.Name] = failure
	}

	for _, set := range current.FlagSets {
		info.FlagSets = append(info.FlagSets, flagSetInfo(set))
	}

	for _, obj := range current.Objects {
		info.Objects = append(info.Objects, objectInfo(obj, failures))
		info.State.KeptFlags += keptFlags(obj)
	}

	for _, w := range current.Withdrawn {
		info.Withdrawn = append(info.Withdrawn, model.WithdrawnInfo{Name: w.Name, Generation: w.Generation})
	}

	slices.SortFunc(info.FlagSets, func(a, b model.FlagSetInfo) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(info.Objects, func(a, b model.ObjectInfo) int { return strings.Compare(a.Name, b.Name) })

	return info
}

func stateInfo(state *manifestreader.StateStatus) model.StateInfo {
	return model.StateInfo{
		Enabled: state.Enabled, LastSaveError: state.LastSaveError, Saves: state.Saves, SaveErrors: state.SaveErrors,
	}
}

// keptFlags counts the rejected flags of the object that keep serving an earlier definition.
func keptFlags(obj *flagruntime.ObjectStatus) int {
	kept := 0

	for _, rejection := range obj.Rejected {
		if rejection.Kept {
			kept++
		}
	}

	return kept
}

func roundInfo(round *relaycore.RoundSummary) model.ReloadInfo {
	if round == nil {
		return model.ReloadInfo{At: nil, Outcome: "", Code: "", Generation: 0}
	}

	at := round.At

	return model.ReloadInfo{At: &at, Outcome: round.Outcome, Code: round.Code, Generation: round.Generation}
}

func staleness(status *relaycore.Status, now time.Time) model.StalenessInfo {
	var out model.StalenessInfo

	if sync := status.Source.Reader.LastSync; !sync.IsZero() {
		out.LastConfirmed = &sync
		out.AgeSeconds = max(0, now.Sub(sync).Seconds())
	}

	if ok := status.Reloads.LastSuccess; !ok.IsZero() {
		out.LastSuccessfulReload = &ok
	}

	return out
}

func flagSetInfo(set *flagruntime.FlagSetStatus) model.FlagSetInfo {
	out := model.FlagSetInfo{
		Name: set.Name, State: setLabel(set.State), Problems: []model.ProblemInfo{},
		Flags: set.Flags, APIKeys: set.APIKeys, Rejected: set.Rejected,
	}

	for _, p := range set.Problems {
		out.Problems = append(out.Problems, model.ProblemInfo{Code: string(p.Code), Subject: p.Subject})
	}

	return out
}

func objectInfo(obj *flagruntime.ObjectStatus, failures map[string]manifestreader.ObjectFailure) model.ObjectInfo {
	// the listed checksum is known for an object that failed to load, and equals the held one when it is ok; for
	// the other states (invalid, pending) the status does not carry it
	failure, failed := failures[obj.Name]
	want, reason := failure.Checksum, failure.Reason

	if !failed && obj.State == flagruntime.ObjectOK {
		want = obj.Checksum
	}

	return model.ObjectInfo{
		Name: obj.Name, Kind: obj.Kind, State: string(obj.State), Reason: reason,
		ChecksumHeld: obj.Checksum, ChecksumListed: want, Flags: obj.Flags, Rejected: len(obj.Rejected),
		Kept: keptFlags(obj),
	}
}
