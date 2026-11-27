package valkeysegment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	nameSScan = "SSCAN"
	nameGet   = "GET"
	notNumber = "the cursor of a scan reply is not a number"
	notList   = "not a cursor and a list of strings"
)

func TestAMalformedPageIsAnErrorNeverTheEndOfTheScan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		raw     string
		wantMsg string
	}{
		{name: "an empty cursor", raw: "*2\r\n$0\r\n\r\n*1\r\n$1\r\na\r\n", wantMsg: notNumber},
		{name: "a cursor that is not a number", raw: "*2\r\n$2\r\nx1\r\n*0\r\n", wantMsg: notNumber},
		{name: "a negative cursor", raw: "*2\r\n$2\r\n-1\r\n*0\r\n", wantMsg: notNumber},
		{name: "a cursor that is not a string", raw: "*2\r\n:0\r\n*0\r\n", wantMsg: notList},
		{name: "members that are not a list", raw: "*2\r\n$1\r\n0\r\n$1\r\na\r\n", wantMsg: notList},
		{name: "a member that is not a string", raw: "*2\r\n$1\r\n0\r\n*2\r\n$1\r\na\r\n:7\r\n", wantMsg: notList},
		{name: "a reply of the wrong length", raw: "*1\r\n$1\r\n0\r\n", wantMsg: notList},
		{
			name:    "a cursor that comes back",
			raw:     "*2\r\n$1\r\n5\r\n*1\r\n$1\r\na\r\n",
			wantMsg: "returned the cursor that it was sent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, prov := snapServer(t, "")
			srv.answerOnly(nameSScan, tt.raw)

			streamErr := firstError(prov.Members(context.Background(), snapAlpha))

			require.ErrorIs(t, streamErr, valkeysegment.ErrProtocol)
			require.ErrorContains(t, streamErr, tt.wantMsg)
			assert.Equal(t, "protocol", prov.Health().Failure, "the health of the provider says so")
		})
	}
}

func TestAMalformedPageKeepsTheLastKnownGood(t *testing.T) {
	t.Parallel()

	srv, prov := snapServer(t, "")
	srv.seed(snapAlpha, []string{"a", "b"})

	holder, err := snapshot.New(snapAlpha, prov, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(context.Background()))

	srv.seed(snapAlpha, []string{"c"}) // the version moved
	srv.answerOnly(nameSScan, "*2\r\n$0\r\n\r\n*0\r\n")

	require.ErrorIs(t, holder.Refresh(context.Background()), valkeysegment.ErrProtocol)
	assert.Equal(t, snapshot.ClassRead, holder.Status().LastError)
	assert.Equal(t, segment.Member, holder.Lookup("a"), "the old set survives")
	assert.Equal(t, segment.NotMember, holder.Lookup("c"))
}

func TestAStoredAbsentIsNotTheAbsentToken(t *testing.T) {
	t.Parallel()

	srv, prov := snapServer(t, "")
	ctx := context.Background()

	missing, err := prov.Version(ctx, snapAlpha)
	require.NoError(t, err)
	assert.Equal(t, segment.AbsentVersion, missing)

	srv.mu.Lock()
	srv.values[snapVerKey] = "absent"
	srv.mu.Unlock()

	stored, err := prov.Version(ctx, snapAlpha)
	require.NoError(t, err)
	assert.NotEqual(t, segment.AbsentVersion, stored, "the writer stored the very word")

	// the holder follows: the set exists under a missing version key, then the writer stores "absent" and replaces it
	srv.setMembers("seg:{alpha}", "a")

	srv.mu.Lock()
	delete(srv.values, snapVerKey)
	srv.mu.Unlock()

	holder, err := snapshot.New(snapAlpha, prov, snapshot.Options{})
	require.NoError(t, err)
	require.NoError(t, holder.Refresh(ctx))
	assert.Equal(t, segment.Member, holder.Lookup("a"))

	srv.setMembers("seg:{alpha}", "b")

	srv.mu.Lock()
	srv.values[snapVerKey] = "absent"
	srv.mu.Unlock()

	require.NoError(t, holder.Refresh(ctx))
	assert.Equal(t, segment.Member, holder.Lookup("b"), "the version moved: the set is pulled again")
}

func TestSnapshotCallsFeedTheHealthOfTheProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		onlyGet    string
		wantFail   string
		wantReady  bool
		cancelCall bool
	}{
		{name: "a call that works", wantReady: true},
		{name: "a server error", onlyGet: "-ERR boom\r\n", wantFail: "server"},
		{name: "an ended context", cancelCall: true, wantFail: "canceled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, prov := snapServer(t, "")
			if tt.onlyGet != "" {
				srv.answerOnly(nameGet, tt.onlyGet)
			}

			ctx := context.Background()

			if tt.cancelCall {
				var cancel context.CancelFunc

				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			_, _ = prov.Version(ctx, snapAlpha)

			health := prov.Health()
			assert.Equal(t, tt.wantFail, health.Failure)
			assert.Equal(t, tt.wantReady, health.Ready)
		})
	}
}

func TestInitProbesTheCommandsOfTheSnapshotMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		refuse      string
		wantErrText string
		wantScans   int
		wantGets    int
		snapshot    bool
	}{
		{name: "lookup only: nothing extra", snapshot: false},
		{name: "snapshot", snapshot: true, wantScans: 1, wantGets: 1},
		{name: "SSCAN refused", snapshot: true, refuse: nameSScan, wantErrText: "the command SSCAN is not available"},
		{name: "GET refused", snapshot: true, refuse: nameGet, wantErrText: "the command GET is not available"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newStubServer(t)
			segs := map[string]valkeysegment.SegmentOptions{
				snapAlpha: {Key: "seg:{alpha}", VersionKey: snapVerKey, Snapshot: tt.snapshot},
			}

			prov, err := valkeysegment.Dial(srv.clientOption(), &valkeysegment.Options{Segments: segs})
			require.NoError(t, err)

			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
				defer cancel()

				_ = prov.Shutdown(ctx)
			})

			if tt.refuse != "" {
				srv.refuse(tt.refuse)
			}

			err = prov.Init(context.Background())

			if tt.wantErrText != "" {
				require.ErrorIs(t, err, valkeysegment.ErrInit)
				require.ErrorIs(t, err, valkeysegment.ErrServer)
				require.ErrorContains(t, err, tt.wantErrText)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantScans, srv.count("sscan"))
			assert.Equal(t, tt.wantGets, srv.count("get"))
		})
	}
}
