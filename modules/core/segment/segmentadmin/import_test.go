package segmentadmin_test

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

const (
	opSADD     = "SADD"
	opSET      = "SET"
	opUnlink   = "UNLINK"
	liveKey    = "semaphore:segment:{vip}"
	versionKey = "semaphore:segment:vip:version"
	tmpKey     = liveKey + ":import:new"
	oldKey     = liveKey + ":import:old"
)

func vipTarget() *segmentadmin.Target {
	return &segmentadmin.Target{Name: "vip", Key: liveKey, VersionKey: versionKey}
}

// cancelAfter is an input of n lines that cancels a context when its last line is read.
type cancelAfter struct {
	cancel context.CancelFunc
	lines  int
	read   int
}

func (c *cancelAfter) Read(buf []byte) (int, error) {
	if c.read >= c.lines {
		return 0, io.EOF
	}

	c.read++
	if c.read == c.lines {
		c.cancel()
	}

	return copy(buf, fmt.Sprintf("id-%d\n", c.read)), nil
}

// importCase is one row of TestImport.
type importCase struct {
	name           string
	input          string
	normalize      resolver.Normalization
	layout         redissegment.Layout
	failOp         string
	wantOrder      []string
	preload        []string
	wantKeys       []string
	wantLive       []string
	wantErrs       []error
	wantRejected   segmentadmin.Rejections
	opts           segmentadmin.ImportOptions
	cancelLines    int
	failNth        int
	wantRead       int64
	wantAdded      int64
	wantDuplicates int64
	maxID          int
	wantSADDCalls  int
	noUnlink       bool
	leftover       bool
	wantSwapped    bool
	wantFellBack   bool
	noVersionKey   bool
}

// prepare builds the store, the target and the input of a row.
func (tt *importCase) prepare(
	ctx context.Context, t *testing.T, cancel context.CancelFunc,
) (*admintest.Fake, *segmentadmin.Target, *segmentadmin.Reader) {
	t.Helper()

	store := admintest.New()
	if len(tt.preload) > 0 {
		_, err := store.SAdd(ctx, liveKey, tt.preload, 0)
		require.NoError(t, err)
	}

	if tt.leftover {
		_, _ = store.SAdd(ctx, tmpKey, []string{"junk"}, 0)
		_, _ = store.SAdd(ctx, oldKey, []string{"junk"}, 0)
	}

	if tt.noUnlink {
		store.WithoutUnlink()
	}

	if tt.failOp != "" {
		store.Fail(tt.failOp, tt.failNth)
	}

	var src io.Reader = strings.NewReader(tt.input)
	if tt.cancelLines > 0 {
		src = &cancelAfter{cancel: cancel, lines: tt.cancelLines}
	}

	reader, err := segmentadmin.NewReader(src, segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	target := vipTarget()
	target.Normalize, target.MaxIDBytes, target.Layout = tt.normalize, tt.maxID, tt.layout

	if tt.noVersionKey {
		target.VersionKey = ""
	}

	return store, target, reader
}

func TestImport(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 9)

	tests := []importCase{
		{
			name: "replaces the old set", input: "a\nb\na\n", preload: []string{"old1", "old2"},
			wantLive: []string{"a", "b"}, wantKeys: []string{liveKey, versionKey}, wantRead: 3, wantAdded: 2,
			wantDuplicates: 1, wantSwapped: true, wantRejected: segmentadmin.Rejections{},
			wantOrder: []string{"SWAP " + liveKey, "SET " + versionKey, "UNLINK " + oldKey},
		},
		{
			name: "normalises as the relay does", input: " A \nb\n", normalize: resolver.NormalizeTrimLower,
			wantLive: []string{"a", "b"}, wantKeys: []string{liveKey, versionKey}, wantRead: 2, wantAdded: 2,
			wantSwapped: true, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "counts what it rejects", input: "ok\n\n" + long + "\n\xff\n", normalize: resolver.NormalizeTrimLower,
			maxID: 5, wantLive: []string{"ok"}, wantKeys: []string{liveKey, versionKey}, wantRead: 4, wantAdded: 1,
			wantSwapped:  true,
			wantRejected: segmentadmin.Rejections{Empty: 1, TooLong: 1, Invalid: 1},
		},
		{
			name: "batches", input: "1\n2\n3\n4\n5\n", opts: segmentadmin.ImportOptions{BatchSize: 2},
			wantLive: []string{"1", "2", "3", "4", "5"}, wantKeys: []string{liveKey, versionKey}, wantRead: 5,
			wantAdded: 5, wantSwapped: true, wantRejected: segmentadmin.Rejections{}, wantSADDCalls: 3,
		},
		{
			name: "no version key", input: "a\n", noVersionKey: true, wantLive: []string{"a"},
			wantKeys: []string{liveKey}, wantRead: 1, wantAdded: 1, wantSwapped: true,
			wantRejected: segmentadmin.Rejections{},
		},
		{
			name:         "an empty input is refused",
			input:        "\n \n",
			preload:      []string{"x"},
			wantErrs:     []error{segmentadmin.ErrEmptyInput},
			wantLive:     []string{"x"},
			wantKeys:     []string{liveKey},
			wantRead:     2,
			wantRejected: segmentadmin.Rejections{Empty: 2},
		},
		{
			name: "an empty input is allowed on request", input: "", preload: []string{"x"},
			opts: segmentadmin.ImportOptions{AllowEmpty: true}, wantKeys: []string{versionKey}, wantSwapped: true,
			wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a dry run writes nothing", input: "a\n\nb\n", preload: []string{"x"},
			opts: segmentadmin.ImportOptions{DryRun: true}, wantLive: []string{"x"}, wantKeys: []string{liveKey},
			wantRead: 3, wantRejected: segmentadmin.Rejections{Empty: 1},
		},
		{
			name: "a dry run refuses an empty input", input: "", opts: segmentadmin.ImportOptions{DryRun: true},
			wantErrs: []error{segmentadmin.ErrEmptyInput}, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "the keys layout is refused", input: "a\n", layout: redissegment.LayoutKeys,
			wantErrs: []error{segmentadmin.ErrKeysLayout},
		},
		{
			name: "a failed batch leaves the live set", input: "a\n", preload: []string{"x"}, failOp: opSADD,
			failNth: 1, wantErrs: []error{admintest.ErrInjected}, wantLive: []string{"x"}, wantKeys: []string{liveKey},
			wantRead: 1, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a failed second batch removes the temporary key", input: "a\nb\nc\nd\n", preload: []string{"x"},
			opts: segmentadmin.ImportOptions{BatchSize: 2}, failOp: opSADD, failNth: 2,
			wantErrs: []error{admintest.ErrInjected}, wantLive: []string{"x"}, wantKeys: []string{liveKey},
			wantRead: 4, wantAdded: 2, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a failed swap leaves the live set", input: "a\n", preload: []string{"x"}, failOp: "SWAP", failNth: 1,
			wantErrs: []error{admintest.ErrInjected}, wantLive: []string{"x"}, wantKeys: []string{liveKey},
			wantRead: 1, wantAdded: 1, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a failed version bump is reported after the swap", input: "a\n", preload: []string{"x"},
			failOp: opSET, failNth: 1, wantErrs: []error{admintest.ErrInjected, segmentadmin.ErrSwapped},
			wantLive: []string{"a"}, wantKeys: []string{liveKey, oldKey}, wantRead: 1, wantAdded: 1,
			wantSwapped: true, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a failed removal of the old set is reported", input: "a\n", preload: []string{"x"},
			failOp: opUnlink, failNth: 3, wantErrs: []error{admintest.ErrInjected, segmentadmin.ErrSwapped},
			wantLive: []string{"a"}, wantKeys: []string{liveKey, oldKey, versionKey}, wantRead: 1, wantAdded: 1,
			wantSwapped: true, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a cancelled run leaves the live set", preload: []string{"x"}, cancelLines: 3,
			wantErrs: []error{context.Canceled}, wantLive: []string{"x"}, wantKeys: []string{liveKey}, wantRead: 3,
			opts: segmentadmin.ImportOptions{BatchSize: 1}, wantAdded: 2, wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "the keys of a killed run are removed", input: "a\n", leftover: true, wantLive: []string{"a"},
			wantKeys: []string{liveKey, versionKey}, wantRead: 1, wantAdded: 1, wantSwapped: true,
			wantRejected: segmentadmin.Rejections{},
		},
		{
			name: "a server without UNLINK gets DEL", input: "a\n", noUnlink: true, wantLive: []string{"a"},
			wantKeys: []string{liveKey, versionKey}, wantRead: 1, wantAdded: 1, wantSwapped: true, wantFellBack: true,
			wantRejected: segmentadmin.Rejections{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			store, target, reader := tt.prepare(ctx, t, cancel)

			calls := len(store.Calls())
			rep, err := segmentadmin.Import(ctx, store, target, reader, tt.opts)

			for _, want := range tt.wantErrs {
				require.ErrorIs(t, err, want)
			}

			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
			}

			if rep != nil {
				assert.Equal(t, tt.wantRead, rep.Read)
				assert.Equal(t, tt.wantAdded, rep.Added)
				assert.Equal(t, tt.wantDuplicates, rep.Duplicates)
				assert.Equal(t, tt.wantRejected, rep.Rejected)
				assert.Equal(t, tt.wantSwapped, rep.Swapped)
				assert.Equal(t, tt.wantFellBack, rep.FellBackToDel)
			}

			if tt.layout == redissegment.LayoutKeys {
				assert.Nil(t, rep)
				assert.Len(t, store.Calls(), calls, "the keys layout is refused before any command")

				return
			}

			assert.Equal(t, nonNil(slices.Sorted(slices.Values(tt.wantKeys))), nonNil(store.Keys()))
			assert.Equal(t, tt.wantLive, nonNil(store.Members(liveKey)))

			if tt.wantSADDCalls > 0 {
				assert.Equal(t, tt.wantSADDCalls, countCalls(store.Calls(), "SADD "+tmpKey))
			}

			if len(tt.wantOrder) > 0 {
				assert.Equal(t, tt.wantOrder, filterCalls(store.Calls(), tt.wantOrder))
			}
		})
	}
}

func nonNil(in []string) []string {
	if len(in) == 0 {
		return nil
	}

	return in
}

func countCalls(calls []string, want string) int {
	return len(slices.DeleteFunc(slices.Clone(calls), func(call string) bool { return call != want }))
}

// filterCalls keeps the calls named in want, in the order they were made; the first UNLINK of the old key is the
// cleanup before the run, so only the last call of each is kept.
func filterCalls(calls, want []string) []string {
	var out []string

	for _, call := range calls {
		if slices.Contains(want, call) {
			out = append(out, call)
		}
	}

	if n := len(out); n > len(want) {
		out = out[n-len(want):]
	}

	return out
}

func TestImport_badOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts segmentadmin.ImportOptions
	}{
		{name: "batch too big", opts: segmentadmin.ImportOptions{BatchSize: segmentadmin.MaxBatchSize + 1}},
		{name: "negative batch", opts: segmentadmin.ImportOptions{BatchSize: -1}},
		{name: "negative ttl", opts: segmentadmin.ImportOptions{TempTTL: -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reader, err := segmentadmin.NewReader(strings.NewReader("a\n"), segmentadmin.ReaderOptions{})
			require.NoError(t, err)

			_, err = segmentadmin.Import(t.Context(), admintest.New(), vipTarget(), reader, tt.opts)
			require.ErrorIs(t, err, segmentadmin.ErrBadOptions)
		})
	}
}

func TestImport_noSlotTag(t *testing.T) {
	t.Parallel()

	reader, err := segmentadmin.NewReader(strings.NewReader("a\n"), segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	_, err = segmentadmin.Import(
		t.Context(),
		admintest.New(),
		&segmentadmin.Target{Key: "{}x"},
		reader,
		segmentadmin.ImportOptions{},
	)
	require.ErrorIs(t, err, segmentadmin.ErrNoSlotTag)
}

func TestImport_tempKeyExpiry(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	_, err := store.SAdd(t.Context(), liveKey, []string{"x"}, 0)
	require.NoError(t, err)

	reader, err := segmentadmin.NewReader(strings.NewReader("a\n"), segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	// a failure of the removal of the old set keeps it, with the ttl that bounds its life
	store.Fail(opUnlink, 3)

	_, err = segmentadmin.Import(
		t.Context(),
		store,
		vipTarget(),
		reader,
		segmentadmin.ImportOptions{TempTTL: 90_000_000_000},
	)
	require.ErrorIs(t, err, segmentadmin.ErrSwapped)

	assert.Equal(t, int64(90_000_000_000), int64(store.TTLOf(oldKey)))
	assert.Zero(t, store.TTLOf(liveKey), "the live set must not inherit the expiry of the temporary key")
}

// dropTemp is an input that removes the temporary key when it ends, as an expiry would.
type dropTemp struct {
	store *admintest.Fake
	done  bool
}

func (d *dropTemp) Read(buf []byte) (int, error) {
	if d.done {
		_, _, _ = d.store.Unlink(context.Background(), tmpKey)

		return 0, io.EOF
	}

	d.done = true

	return copy(buf, "a\n"), nil
}

func TestImport_tempGone(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	_, err := store.SAdd(t.Context(), liveKey, []string{"x"}, 0)
	require.NoError(t, err)

	reader, err := segmentadmin.NewReader(&dropTemp{store: store}, segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	_, err = segmentadmin.Import(t.Context(), store, vipTarget(), reader, segmentadmin.ImportOptions{BatchSize: 1})
	require.ErrorIs(t, err, segmentadmin.ErrTempGone)
	assert.Equal(t, []string{"x"}, store.Members(liveKey), "nothing was swapped")
}
