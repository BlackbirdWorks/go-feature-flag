package valkeysegment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	snapAlpha  = "alpha"
	snapVerKey = "seg:alpha:version"
)

// snapServer starts a stub server and a provider with the version key of every segment of the suite.
func snapServer(tb testing.TB, miss valkeysegment.MissingKey) (*stubServer, *valkeysegment.Provider) {
	tb.Helper()

	srv := newStubServer(tb)
	segs := map[string]valkeysegment.SegmentOptions{}

	for _, name := range segmenttest.SnapshotSegments() {
		segs[name] = valkeysegment.SegmentOptions{Key: "seg:{" + name + "}", VersionKey: "seg:" + name + ":version"}
	}

	segs["keyed"] = valkeysegment.SegmentOptions{Layout: valkeysegment.LayoutKeys, Key: "k:"}
	segs["nover"] = valkeysegment.SegmentOptions{}

	prov, err := valkeysegment.Dial(srv.clientOption(), &valkeysegment.Options{Segments: segs, MissingKey: miss})
	require.NoError(tb, err)

	tb.Cleanup(func() {
		srv.Release()

		ctx, cancel := context.WithTimeout(context.Background(), shutdownBound)
		defer cancel()

		_ = prov.Shutdown(ctx)
	})

	return srv, prov
}

func seedOf(srv *stubServer) segmenttest.Seed {
	return func(name string, ids []string) error {
		srv.seed(name, ids)

		return nil
	}
}

//nolint:paralleltest // the suite measures the allocations of the whole process in one case
func TestSnapshotSourceSuite(t *testing.T) {
	segmenttest.RunSnapshotSource(t, func() (segmenttest.SnapshotFixture, error) {
		srv, prov := snapServer(t, "")

		return segmenttest.SnapshotFixture{Source: prov, Seed: seedOf(srv), NoEmptySet: true}, nil
	})
}

func TestVersionerSuite(t *testing.T) {
	t.Parallel()

	segmenttest.RunVersioner(t, func() (segmenttest.VersionerFixture, error) {
		srv, prov := snapServer(t, "")

		return segmenttest.VersionerFixture{Versioner: prov, Seed: seedOf(srv)}, nil
	})
}

func TestMembersArePagedWithSSCAN(t *testing.T) {
	t.Parallel()

	srv, prov := snapServer(t, "")
	srv.pageSize = 3
	srv.seed(snapAlpha, []string{"a", "b", "c", "d", "e", "f", "g"})

	var got []string

	for id, err := range prov.Members(context.Background(), snapAlpha) {
		require.NoError(t, err)

		got = append(got, id)
	}

	assert.ElementsMatch(t, []string{"a", "b", "c", "d", "e", "f", "g"}, got)
	assert.Equal(t, 3, srv.count("sscan"), "7 members in pages of 3 are 3 pages")
}

func TestASegmentWithNoMemberIsMissingWhateverTheProviderPolicy(t *testing.T) {
	t.Parallel()

	for _, miss := range []valkeysegment.MissingKey{"", valkeysegment.MissingUnknown, valkeysegment.MissingNotMember} {
		t.Run(string(miss), func(t *testing.T) {
			t.Parallel()

			_, prov := snapServer(t, miss)

			var items []string

			for id, err := range prov.Members(context.Background(), snapAlpha) {
				items = append(items, id)

				require.ErrorIs(t, err, segment.ErrSegmentMissing)
			}

			assert.Equal(t, []string{""}, items, "one element, the error")
		})
	}
}

func firstError(seq func(func(string, error) bool)) error {
	var out error

	seq(func(_ string, err error) bool {
		out = err

		return err == nil
	})

	return out
}

func TestSnapshotRefusalsAndVersion(t *testing.T) {
	t.Parallel()

	srv, prov := snapServer(t, "")
	ctx := context.Background()

	require.ErrorIs(t, firstError(prov.Members(ctx, "keyed")), valkeysegment.ErrSnapshotLayout)
	require.ErrorIs(t, firstError(prov.Members(ctx, "ghost-set")), segment.ErrSegmentMissing)

	_, err := prov.Version(ctx, "nover")
	require.ErrorIs(t, err, valkeysegment.ErrNoVersionKey)

	_, err = prov.Version(ctx, "ghost-set")
	require.ErrorIs(t, err, segment.ErrSegmentMissing)

	absent, err := prov.Version(ctx, snapAlpha)
	require.NoError(t, err)
	assert.Equal(t, segment.AbsentVersion, absent, "a missing version key is the absent token, not an error")

	srv.mu.Lock()
	srv.values[snapVerKey] = "v42"
	srv.mu.Unlock()

	token, err := prov.Version(ctx, snapAlpha)
	require.NoError(t, err)
	assert.Equal(t, "v:v42", token)
}

func TestSnapshotFailures(t *testing.T) {
	t.Parallel()

	srv, prov := snapServer(t, "")
	ctx := context.Background()

	srv.answerOnly("GET", "$abc\r\n")

	_, err := prov.Version(ctx, snapAlpha)
	require.Error(t, err, "a reply that is not RESP fails the call")

	srv.answerOnly("SSCAN", "+nope\r\n")
	require.Error(t, firstError(prov.Members(ctx, snapAlpha)), "a reply of the wrong shape fails the stream")

	require.NoError(t, prov.Shutdown(ctx))

	_, err = prov.Version(ctx, snapAlpha)
	require.ErrorIs(t, err, valkeysegment.ErrClosed)
	require.ErrorIs(t, firstError(prov.Members(ctx, snapAlpha)), valkeysegment.ErrClosed)
}

func TestTheDefaultMeaningOfAMissingSetIsNoMember(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		miss      valkeysegment.MissingKey
		wantState segment.State
	}{
		{name: "default", miss: "", wantState: segment.NotMember},
		{name: "notMember", miss: valkeysegment.MissingNotMember, wantState: segment.NotMember},
		{name: "unknown opt-in", miss: valkeysegment.MissingUnknown, wantState: segment.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, prov := snapServer(t, tt.miss) // no set exists in the store
			lookups := []segment.Lookup{{Segment: snapAlpha, ID: "a"}}

			require.NoError(t, prov.Contains(context.Background(), lookups))
			assert.Equal(t, tt.wantState, lookups[0].State)
		})
	}
}
