//go:build docker

package valkeysegment_test

import (
	"context"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/internal/testproxy"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	replaceMembers   = 20_000
	replaceSampleGap = 1_000
	replacePageDelay = 15 * time.Millisecond
	replaceScanPages = 3
	replaceTimeout   = 2 * time.Minute
	// replaceDebounce gives a quiet step of 200ms, long enough for the importer of the test to be done.
	replaceDebounce = 2 * time.Second
	steadyDebounce  = time.Second
	steadyRefreshes = 3
	steadyEditEvery = 2 * time.Millisecond
	// steadyPageSlack is the pages that the edits of the test add to a pull of the set (it grows by a few thousand).
	steadyPageSlack = 5
)

// servedSet tells what one snapshot of the holder is: the sampled ids of the old set, of the new set, both or neither.
func servedSet(holder *snapshot.Holder, lookups []segment.Lookup) (int, int, error) {
	probe := make([]segment.Lookup, len(lookups))
	copy(probe, lookups)

	err := holder.Contains(context.Background(), probe)
	if err != nil {
		return 0, 0, err
	}

	var olds, news int

	for i := range probe {
		if probe[i].State != segment.Member {
			continue
		}

		if probe[i].ID[:3] == "old" {
			olds++
		} else {
			news++
		}
	}

	return olds, news, nil
}

// A set that is replaced (RENAME, then the version bump) while a slow scan of it runs: with a debounce every snapshot
// of the holder is exactly the old set or exactly the new one, and the refresh ends on the new one under its version;
// with no debounce (the default) the first pull is installed under the version read before it, and the next refresh
// pulls the new set.
func TestASetReplacedDuringASlowScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		wantVersion string
		debounce    time.Duration
		wantNoMix   bool
	}{
		{name: "debounce on: never served mixed", debounce: replaceDebounce, wantVersion: "v:3", wantNoMix: true},
		{name: "debounce off: the first pull is installed", wantVersion: "v:2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			replaceDuringScan(t, tt.debounce, tt.wantVersion, tt.wantNoMix)
		})
	}
}

func replaceDuringScan(t *testing.T, debounce time.Duration, wantVersion string, wantNoMix bool) {
	t.Helper()

	srv := startServerImage(t, valkeyImage)
	writer := seed(t, srv.defaultOption())
	ctx := t.Context()

	const (
		setKey     = "replace:{alpha}"
		stagingKey = "replace:{alpha}:new"
		versionKey = "replace:alpha:version"
	)

	proxy, err := testproxy.Start(srv.address)
	require.NoError(t, err)
	t.Cleanup(proxy.Close)

	prov, err := valkeysegment.Dial(&valkey.ClientOption{InitAddress: []string{proxy.Addr()}}, &valkeysegment.Options{
		Segments: map[string]valkeysegment.SegmentOptions{
			snapAlpha: {Key: setKey, VersionKey: versionKey},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
		defer cancel()

		_ = prov.Shutdown(shutCtx)
	})

	ids := func(prefix string) []string {
		out := make([]string, replaceMembers)
		for i := range out {
			out[i] = prefix + "-" + strconv.Itoa(i)
		}

		return out
	}

	add := func(key, prefix string) {
		members := ids(prefix)

		for start := 0; start < len(members); start += seedChunk {
			require.NoError(t, writer.Do(ctx, writer.B().Sadd().Key(key).
				Member(members[start:min(start+seedChunk, len(members))]...).Build()).Error())
		}
	}

	bump := func(version string) {
		require.NoError(t, writer.Do(ctx, writer.B().Set().Key(versionKey).Value(version).Build()).Error())
	}

	add(setKey, "old")
	bump("1")

	holder, err := snapshot.New(snapAlpha, prov, snapshot.Options{Debounce: debounce})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(ctx))
	require.Equal(t, replaceMembers, holder.Status().Members)

	// the writer touches the version, so the next refresh pulls the old members again, slowly
	bump("2")
	proxy.SetDelay(replacePageDelay)

	lookups := make([]segment.Lookup, 0, 2*(replaceMembers/replaceSampleGap+1))

	for i := 0; i < replaceMembers; i += replaceSampleGap {
		lookups = append(lookups,
			segment.Lookup{Segment: snapAlpha, ID: "old-" + strconv.Itoa(i)},
			segment.Lookup{Segment: snapAlpha, ID: "new-" + strconv.Itoa(i)},
		)
	}

	base := calls(t, writer, "sscan")
	done := make(chan struct{})
	refreshed := make(chan error, 1)

	var observers sync.WaitGroup

	observers.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}

			olds, news, serveErr := servedSet(holder, lookups)
			assert.Equal(t, error(nil), serveErr)

			isOld := olds == len(lookups)/2 && news == 0
			isNew := news == len(lookups)/2 && olds == 0
			assert.True(t, isOld || isNew || !wantNoMix, "served %d old and %d new sampled members", olds, news)

			runtime.Gosched()
		}
	})

	go func() { refreshed <- holder.Refresh(ctx) }()

	// the scan is under way (pages of the old set were read): replace the set and bump the version, as an importer does
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, calls(t, writer, "sscan"), base+replaceScanPages)
	}, replaceTimeout, replacePageDelay)

	add(stagingKey, "new")
	require.NoError(t, writer.Do(ctx, writer.B().Rename().Key(stagingKey).Newkey(setKey).Build()).Error())
	bump("3")

	require.NoError(t, <-refreshed)
	close(done)
	observers.Wait()

	status := holder.Status()
	if wantNoMix {
		assert.Equal(t, replaceMembers, status.Members)
	}

	assert.Equal(t, wantVersion, status.Version, "the version that the installed set is known under")
	assert.Equal(t, snapshot.ClassNone, status.LastError)

	if !wantNoMix {
		// the next poll reads the new version and pulls the new set
		proxy.SetDelay(0)
		require.NoError(t, holder.Refresh(ctx))
	}

	olds, news, err := servedSet(holder, lookups)
	require.NoError(t, err)
	assert.Equal(t, 0, olds)
	assert.Equal(t, len(lookups)/2, news)
	assert.Equal(t, "v:3", holder.Status().Version)
	assert.Equal(t, replaceMembers, holder.Status().Members)
}

// A set that is edited all the time (SADD then a version bump, in a loop) while it is pulled slowly: each refresh
// costs two pulls at most, ends at the debounce with the latest pull installed, and the holder is never stale.
func TestASetEditedWithoutPauseIsRefreshedAtTheDebounce(t *testing.T) {
	t.Parallel()

	srv := startServerImage(t, valkeyImage)
	writer := seed(t, srv.defaultOption())
	ctx := t.Context()

	const (
		setKey     = "steady:{alpha}"
		versionKey = "steady:alpha:version"
	)

	proxy, err := testproxy.Start(srv.address)
	require.NoError(t, err)
	t.Cleanup(proxy.Close)

	prov, err := valkeysegment.Dial(&valkey.ClientOption{InitAddress: []string{proxy.Addr()}}, &valkeysegment.Options{
		Segments: map[string]valkeysegment.SegmentOptions{
			snapAlpha: {Key: setKey, VersionKey: versionKey},
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
		defer cancel()

		_ = prov.Shutdown(shutCtx)
	})

	members := make([]string, replaceMembers)
	for i := range members {
		members[i] = "m-" + strconv.Itoa(i)
	}

	for start := 0; start < len(members); start += seedChunk {
		require.NoError(t, writer.Do(ctx, writer.B().Sadd().Key(setKey).
			Member(members[start:min(start+seedChunk, len(members))]...).Build()).Error())
	}

	require.NoError(t, writer.Do(ctx, writer.B().Set().Key(versionKey).Value("1").Build()).Error())

	holder, err := snapshot.New(snapAlpha, prov, snapshot.Options{Debounce: steadyDebounce})
	require.NoError(t, err)

	proxy.SetDelay(replacePageDelay)

	// one pull of a quiet set: the number of scan calls that it costs
	before := calls(t, writer, "sscan")
	require.NoError(t, holder.Refresh(ctx))

	onePull := calls(t, writer, "sscan") - before
	require.Positive(t, onePull)

	// a write that is under way when the edits stop is not an error: the stop only ends the loop
	writeCtx := context.WithoutCancel(ctx)
	editCtx, stop := context.WithCancel(ctx)
	edits := make(chan struct{})

	go func() {
		defer close(edits)

		tick := time.NewTicker(steadyEditEvery)
		defer tick.Stop()

		for edit := 0; ; edit++ {
			select {
			case <-editCtx.Done():
				return
			case <-tick.C:
			}

			assert.NoError(
				t,
				writer.Do(writeCtx, writer.B().Sadd().Key(setKey).Member("edit-"+strconv.Itoa(edit)).Build()).Error(),
			)
			assert.NoError(t, writer.Do(writeCtx, writer.B().Incr().Key(versionKey).Build()).Error())
		}
	}()

	t.Cleanup(func() {
		stop()
		<-edits
	})

	for round := 1; round <= steadyRefreshes; round++ {
		before = calls(t, writer, "sscan")

		require.NoError(t, holder.Refresh(ctx), "round %d", round)

		// the pulls of a refresh: the first and the last, not one for each edit (the set grows a little)
		assert.LessOrEqual(t, calls(t, writer, "sscan")-before, 2*(onePull+steadyPageSlack), "round %d", round)

		status := holder.Status()
		assert.Equal(t, snapshot.ClassNone, status.LastError)
		assert.Equal(t, uint64(round), status.Unsettled, "each refresh was installed without a stable version")
		assert.False(t, holder.Stale())

		_, answer := holder.Answer("m-1")
		assert.Equal(t, segment.StatusHealthy, answer)
	}

	assert.GreaterOrEqual(t, holder.Status().Members, replaceMembers)
}
