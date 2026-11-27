package corehttp

import (
	"slices"
	"strings"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/relaycore"
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
	rt := m.holder.Load()
	dates := make(map[string]time.Time, len(rt.FlagSets))

	var latest time.Time

	for name, set := range rt.FlagSets {
		dates[name] = set.ModifiedAt
		if set.ModifiedAt.After(latest) {
			latest = set.ModifiedAt
		}
	}

	cfg := configInfo(m.src.Status(), clock.Now())

	return model.InfoResponse{Flagsets: dates, LatestCacheRefresh: &latest, Config: &cfg}, nil
}

func configInfo(st relaycore.Status, now time.Time) model.ConfigInfo {
	info := model.ConfigInfo{
		Reloads: map[string]uint64{
			string(flagruntime.OutcomeApplied):  st.Reloads.Applied,
			string(flagruntime.OutcomeRejected): st.Reloads.Rejected,
			string(flagruntime.OutcomeIgnored):  st.Reloads.Ignored,
		},
		FlagSets: []model.FlagSetInfo{}, Objects: []model.ObjectInfo{}, Withdrawn: []model.WithdrawnInfo{},
		PendingRestart: []string{},
		Staleness:      staleness(st, now),
	}

	round := st.Source.Round
	rt := st.Source.Runtime

	if rt == nil {
		info.LastReload = reloadInfo(&round.Reload)

		return info
	}

	info.Generation, info.Snapshot = rt.Generation, rt.Snapshot
	info.LastReload = reloadInfo(&rt.LastReload)
	info.PendingRestart = append(info.PendingRestart, rt.PendingRestart...)

	switch {
	case round.Outcome == flagruntime.OutcomeRejected:
		rejected := reloadInfo(&round.Reload)
		info.LastRejected = &rejected
	case rt.LastRejected != nil:
		rejected := reloadInfo(rt.LastRejected)
		info.LastRejected = &rejected
	}

	listed := map[string]string{}
	for _, failure := range st.Source.Reader.Failures {
		listed[failure.Name] = failure.Checksum
	}

	for _, set := range rt.FlagSets {
		info.FlagSets = append(info.FlagSets, flagSetInfo(set))
	}

	for _, obj := range rt.Objects {
		info.Objects = append(info.Objects, objectInfo(obj, listed))
	}

	for _, w := range rt.Withdrawn {
		info.Withdrawn = append(info.Withdrawn, model.WithdrawnInfo{Name: w.Name, Generation: w.Generation})
	}

	slices.SortFunc(info.FlagSets, func(a, b model.FlagSetInfo) int { return strings.Compare(a.Name, b.Name) })
	slices.SortFunc(info.Objects, func(a, b model.ObjectInfo) int { return strings.Compare(a.Name, b.Name) })

	return info
}

func reloadInfo(r *flagruntime.Reload) model.ReloadInfo {
	return model.ReloadInfo{Outcome: string(r.Outcome), Code: r.Code, Detail: r.Detail, Generation: r.Generation}
}

func staleness(st relaycore.Status, now time.Time) model.StalenessInfo {
	var out model.StalenessInfo

	if sync := st.Source.Reader.LastSync; !sync.IsZero() {
		out.LastConfirmed = &sync
		out.AgeSeconds = max(0, now.Sub(sync).Seconds())
	}

	if ok := st.Reloads.LastSuccess; !ok.IsZero() {
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
		out.Problems = append(out.Problems, model.ProblemInfo{Code: string(p.Code), Subject: p.Subject, Reason: p.Reason})
	}

	return out
}

func objectInfo(obj *flagruntime.ObjectStatus, listed map[string]string) model.ObjectInfo {
	// the listed checksum is known for an object that failed to load, and equals the held one when it is ok; for
	// the other states (invalid, pending) the status does not carry it
	want, failed := listed[obj.Name]
	if !failed && obj.State == flagruntime.ObjectOK {
		want = obj.Checksum
	}

	return model.ObjectInfo{
		Name: obj.Name, Kind: obj.Kind, State: string(obj.State), Reason: obj.Reason,
		ChecksumHeld: obj.Checksum, ChecksumListed: want, Flags: obj.Flags, Rejected: len(obj.Rejected),
	}
}
