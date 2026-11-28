package redissegment_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmenttest"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

const (
	snapAlpha   = "alpha"
	snapSetKey  = "seg:{alpha}"
	snapVerKey  = "seg:alpha:version"
	snapMissing = "ghost-set"
)

// snapStore is a fake store with the version key of every segment of the suite.
func snapStore(pageSize int) (*fakeStore, map[string]redissegment.SegmentOptions) {
	store := &fakeStore{
		sets: map[string]map[string]bool{}, keys: map[string]bool{}, values: map[string]string{},
		pageSize: pageSize, honourCtx: true,
	}
	segs := map[string]redissegment.SegmentOptions{}

	for _, name := range segmenttest.SnapshotSegments() {
		segs[name] = redissegment.SegmentOptions{Key: "seg:{" + name + "}", VersionKey: "seg:" + name + ":version"}
	}

	return store, segs
}

// seeder writes a set and bumps the version key, as the writer of a segment does.
func seeder(store *fakeStore) segmenttest.Seed {
	counter := 0

	return func(name string, ids []string) error {
		store.mu.Lock()
		defer store.mu.Unlock()

		key := "seg:{" + name + "}"
		if len(ids) == 0 {
			delete(store.sets, key)
		} else {
			store.sets[key] = map[string]bool{}
			for _, id := range ids {
				store.sets[key][id] = true
			}
		}

		counter++
		store.values["seg:"+name+":version"] = "v" + strconv.Itoa(counter)

		return nil
	}
}

func snapProvider(
	t *testing.T,
	store *fakeStore,
	segs map[string]redissegment.SegmentOptions,
	miss redissegment.MissingKey,
) *redissegment.Provider {
	t.Helper()

	prov, err := redissegment.New(store, &redissegment.Options{Segments: segs, MissingKey: miss})
	require.NoError(t, err)

	return prov
}

//nolint:paralleltest // the suite measures the allocations of the whole process in one case
func TestSnapshotSourceSuite(t *testing.T) {
	segmenttest.RunSnapshotSource(t, func() (segmenttest.SnapshotFixture, error) {
		store, segs := snapStore(0)
		prov, err := redissegment.New(store, &redissegment.Options{Segments: segs})
		require.NoError(t, err)

		return segmenttest.SnapshotFixture{Source: prov, Seed: seeder(store), NoEmptySet: true}, nil
	})
}

func TestVersionerSuite(t *testing.T) {
	t.Parallel()

	segmenttest.RunVersioner(t, func() (segmenttest.VersionerFixture, error) {
		store, segs := snapStore(0)
		prov, err := redissegment.New(store, &redissegment.Options{Segments: segs})
		require.NoError(t, err)

		return segmenttest.VersionerFixture{Versioner: prov, Seed: seeder(store)}, nil
	})
}

func TestMembersArePagedWithSSCAN(t *testing.T) {
	t.Parallel()

	const wantPageSize = 3

	store, segs := snapStore(wantPageSize)
	require.NoError(t, seeder(store)(snapAlpha, []string{"a", "b", "c", "d", "e", "f", "g"}))

	var got []string

	for id, err := range snapProvider(t, store, segs, "").Members(context.Background(), snapAlpha) {
		require.NoError(t, err)

		got = append(got, id)
	}

	assert.ElementsMatch(t, []string{"a", "b", "c", "d", "e", "f", "g"}, got)
	assert.Equal(t, [][]string{
		{"SSCAN " + snapSetKey + " 0 COUNT 1000"},
		{"SSCAN " + snapSetKey + " 3 COUNT 1000"},
		{"SSCAN " + snapSetKey + " 6 COUNT 1000"},
	}, store.commands(), "one command per page, the cursor of the reply is the cursor of the next")
}

func TestStoppingTheStreamStopsTheScan(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(2)
	require.NoError(t, seeder(store)(snapAlpha, []string{"a", "b", "c", "d", "e"}))

	for id, err := range snapProvider(t, store, segs, "").Members(context.Background(), snapAlpha) {
		require.NoError(t, err)
		assert.NotEmpty(t, id)

		break
	}

	assert.Len(t, store.commands(), 1, "no page after the consumer stopped")
}

func TestASegmentWithNoMemberIsMissingWhateverTheProviderPolicy(t *testing.T) {
	t.Parallel()

	for _, miss := range []redissegment.MissingKey{"", redissegment.MissingUnknown, redissegment.MissingNotMember} {
		t.Run(string(miss), func(t *testing.T) {
			t.Parallel()

			store, segs := snapStore(0)

			var items []string

			for id, err := range snapProvider(t, store, segs, miss).Members(context.Background(), snapAlpha) {
				items = append(items, id)

				require.ErrorIs(t, err, segment.ErrSegmentMissing)
			}

			assert.Equal(t, []string{""}, items, "one element, the error")
		})
	}
}

func TestSnapshotRefusals(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	segs["keyed"] = redissegment.SegmentOptions{Layout: redissegment.LayoutKeys, Key: "k:"}
	segs["nover"] = redissegment.SegmentOptions{}
	prov := snapProvider(t, store, segs, "")

	tests := []struct {
		call    func() error
		wantErr error
		name    string
	}{
		{
			name: "the keys layout has no snapshot", wantErr: redissegment.ErrSnapshotLayout,
			call: func() error {
				for _, err := range prov.Members(context.Background(), "keyed") {
					return err
				}

				return nil
			},
		},
		{
			name: "a segment without a version key", wantErr: redissegment.ErrNoVersionKey,
			call: func() error { return versionErr(prov, "nover") },
		},
		{
			name: "an unconfigured segment has no version", wantErr: segment.ErrSegmentMissing,
			call: func() error { return versionErr(prov, snapMissing) },
		},
		{
			name: "an unconfigured segment has no members", wantErr: segment.ErrSegmentMissing,
			call: func() error {
				for _, err := range prov.Members(context.Background(), snapMissing) {
					return err
				}

				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, tt.call(), tt.wantErr)
		})
	}
}

func TestVersionReadsTheVersionKey(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	prov := snapProvider(t, store, segs, "")

	absent, err := prov.Version(context.Background(), snapAlpha)
	require.NoError(t, err)
	assert.Equal(t, segment.AbsentVersion, absent, "a missing version key is the absent token, not an error")

	store.values[snapVerKey] = "v42"

	token, err := prov.Version(context.Background(), snapAlpha)
	require.NoError(t, err)
	assert.Equal(t, "v:v42", token)
	assert.Equal(t, [][]string{{"GET " + snapVerKey}, {"GET " + snapVerKey}}, store.commands())
}

var errServer = errors.New("ERR boom")

func TestSnapshotFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		reply   *fakeReply
		call    func(*redissegment.Provider) error
		wantErr error
		name    string
	}{
		{
			name:    "GET fails",
			reply:   &fakeReply{err: errServer},
			wantErr: errServer,
			call:    func(p *redissegment.Provider) error { return versionErr(p, snapAlpha) },
		},
		{
			name:    "GET returns a non-string",
			reply:   &fakeReply{value: []any{}},
			wantErr: redissegment.ErrProtocol,
			call:    func(p *redissegment.Provider) error { return versionErr(p, snapAlpha) },
		},
		{
			name:    "SSCAN fails",
			reply:   &fakeReply{err: errServer},
			wantErr: errServer,
			call:    func(p *redissegment.Provider) error { return firstError(p.Members(context.Background(), snapAlpha)) },
		},
		{
			name:    "SSCAN reply of the wrong shape",
			reply:   &fakeReply{value: "nope"},
			wantErr: redissegment.ErrProtocol,
			call:    func(p *redissegment.Provider) error { return firstError(p.Members(context.Background(), snapAlpha)) },
		},
		{
			name:    "SSCAN reply with a non-string member",
			reply:   &fakeReply{value: []any{"0", []any{int64(1)}}},
			wantErr: redissegment.ErrProtocol,
			call:    func(p *redissegment.Provider) error { return firstError(p.Members(context.Background(), snapAlpha)) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, segs := snapStore(0)
			store.reply = func([]any) *fakeReply { return tt.reply }

			require.ErrorIs(t, tt.call(snapProvider(t, store, segs, "")), tt.wantErr)
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

func TestSnapshotAfterShutdown(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	prov := snapProvider(t, store, segs, "")
	require.NoError(t, prov.Shutdown(context.Background()))

	_, err := prov.Version(context.Background(), snapAlpha)
	require.ErrorIs(t, err, redissegment.ErrClosed)
	require.ErrorIs(t, firstError(prov.Members(context.Background(), snapAlpha)), redissegment.ErrClosed)
}

func versionErr(prov *redissegment.Provider, name string) error {
	_, err := prov.Version(context.Background(), name)

	return err
}

func TestTheDefaultMeaningOfAMissingSetIsNoMember(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		miss      redissegment.MissingKey
		wantState segment.State
	}{
		{name: "default", miss: "", wantState: segment.NotMember},
		{name: "notMember", miss: redissegment.MissingNotMember, wantState: segment.NotMember},
		{name: "unknown opt-in", miss: redissegment.MissingUnknown, wantState: segment.Unknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, segs := snapStore(0) // no set exists in the store
			lookups := []segment.Lookup{{Segment: snapAlpha, ID: "a"}}

			require.NoError(t, snapProvider(t, store, segs, tt.miss).Contains(context.Background(), lookups))
			assert.Equal(t, tt.wantState, lookups[0].State)
		})
	}
}

const scanNover = "SSCAN seg:{nover} 0 COUNT 1000"

func TestWithoutAVersionKeyTheHolderPullsOnEveryRefresh(t *testing.T) {
	t.Parallel()

	store, segs := snapStore(0)
	segs["nover"] = redissegment.SegmentOptions{Key: "seg:{nover}"}
	store.sets["seg:{nover}"] = map[string]bool{"a": true}

	holder, err := snapshot.New("nover", snapProvider(t, store, segs, ""), snapshot.Options{})
	require.NoError(t, err)

	for range 3 {
		require.NoError(t, holder.Refresh(context.Background()))
	}

	assert.Equal(t, [][]string{
		{scanNover}, {scanNover}, {scanNover},
	}, store.commands(), "no GET of a version, one scan for each refresh")
	assert.Equal(t, segment.Member, holder.Lookup("a"))
}
