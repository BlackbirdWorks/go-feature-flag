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

// A set that is replaced (RENAME, then the version bump) while a slow scan of it runs is never served as a mix: every
// snapshot of the holder is exactly the old set or exactly the new one, and the refresh ends on the new one.
func TestASetReplacedDuringASlowScanIsNeverServedMixed(t *testing.T) {
	t.Parallel()

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

	holder, err := snapshot.New(snapAlpha, prov, snapshot.Options{})
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
			assert.True(t, isOld || isNew, "served %d old and %d new sampled members", olds, news)

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

	olds, news, err := servedSet(holder, lookups)
	require.NoError(t, err)
	assert.Equal(t, 0, olds)
	assert.Equal(t, len(lookups)/2, news)

	status := holder.Status()
	assert.Equal(t, replaceMembers, status.Members)
	assert.Equal(t, "v:3", status.Version, "the set is installed under the version of the new set")
	assert.Equal(t, snapshot.ClassNone, status.LastError)
}
