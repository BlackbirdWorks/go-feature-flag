package storetest

import (
	"bytes"
	"encoding/json"
	"runtime"
	"runtime/pprof"
	"strings"
)

// How leaks are found.
//
// Every case runs with the pprof labels storetest-run (one id per [Run]) and storetest-case (the case). The Go
// runtime copies the labels of a goroutine to the goroutines it starts, so everything a case causes carries them:
// the goroutines of the case itself, those the store starts, those of the SDK and of the HTTP transport. When the
// case is over, the goroutine profile (debug=1, which prints the labels) is read and the goroutines of the case
// that are still there are the leaks. Goroutines of other cases, of other runs and of the test framework have
// other labels and do not count, so parallel subtests do not disturb each other.
//
// The limit: only goroutines started, directly or through others, by a goroutine of the case carry its labels. A
// worker that the store started before the case (a pool, a background refresher) and that does the work of a Get
// on its own goroutines is not attributed to the case, and a goroutine it leaks is not found. A store with such
// workers has to check them itself (stop them in the cleanup of its fixture, and make the fixture fail when they
// do not end).
//
// A goroutine that ends needs a moment to be gone (a cancelled request closes its connection, the connection
// goroutine sees it). The check does not sleep and does not read a clock: it reads the profile, yields the
// processor a bounded number of times, and reads it again, up to settleRounds times. A leak costs that bound
// (about half a second); a case without one is done after the first read or two.
//
// What does not count: the reader and writer goroutines of an open keep-alive HTTP connection. They belong to the
// connection pool of the transport, which outlives any request, and they are created by whichever case dialled
// first. A connection that a cancelled request closed takes its goroutines with it. Only a reader that waits for
// the next response (it is reading the connection, in bufio's Peek) is skipped, with the idle writer: a reader that
// waits for the body of an answer to be read or closed is a store that kept a response body open, and counts.
const (
	settleRounds    = 500
	yieldsPerRound  = 200
	profileDebugLvl = 1
)

// settledLeaks returns the leaked goroutines of the run (and of the case, when caseID is not empty), after giving
// them the bounded number of yields to end.
func settledLeaks(runID, caseID string) []string {
	var leaks []string

	for range settleRounds {
		leaks = leakedGoroutines(runID, caseID)
		if len(leaks) == 0 {
			return nil
		}

		for range yieldsPerRound {
			runtime.Gosched()
		}
	}

	return leaks
}

// leakedGoroutines reads the goroutine profile once and returns the stacks of the goroutines that carry the labels
// and are not exempt.
func leakedGoroutines(runID, caseID string) []string {
	var profile bytes.Buffer

	_ = pprof.Lookup("goroutine").WriteTo(&profile, profileDebugLvl)

	var out []string

	for group := range strings.SplitSeq(profile.String(), "\n\n") {
		labels := groupLabels(group)
		if labels[labelRun] != runID || (caseID != "" && labels[labelCase] != caseID) {
			continue
		}

		if exempt(group) {
			continue
		}

		out = append(out, group)
	}

	return out
}

// exempt reports a goroutine of the keep-alive connection pool of the HTTP transport that waits for work: the
// writer, and the reader while it waits for the next response on the connection.
func exempt(group string) bool {
	if strings.Contains(group, "net/http.(*persistConn).writeLoop") {
		return true
	}

	return strings.Contains(group, "net/http.(*persistConn).readLoop") &&
		strings.Contains(group, "bufio.(*Reader).Peek")
}

// groupLabels reads the "# labels: {...}" line of one group of the profile.
func groupLabels(group string) map[string]string {
	for line := range strings.SplitSeq(group, "\n") {
		raw, found := strings.CutPrefix(line, "# labels: ")
		if !found {
			continue
		}

		var labels map[string]string

		err := json.Unmarshal([]byte(raw), &labels)
		if err == nil {
			return labels
		}
	}

	return nil
}
