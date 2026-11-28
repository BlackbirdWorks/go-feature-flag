package segmentadmin_test

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

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
	opGET      = "GET"
	liveKey    = "semaphore:segment:{vip}"
	versionKey = "semaphore:segment:vip:version"
	runToken   = "t1"
	tmpKey     = liveKey + ":import:" + runToken + ":new"
	oldKey     = liveKey + ":import:" + runToken + ":old"
	lockKey    = liveKey + ":import:lock"
	lastKey    = liveKey + ":import:last"
	otherTmp   = liveKey + ":import:other:new"
	deadTmp    = liveKey + ":import:dead:new"
	otherToken = "other"
)

func vipTarget() *segmentadmin.Target {
	return &segmentadmin.Target{Name: "vip", Key: liveKey, VersionKey: versionKey}
}

// genSource is an input of n ids, "id-<i>", one for each line. It calls onLine(i) when it starts line i, and
// onLine(n+1) when the input ends.
type genSource struct {
	onLine func(line int)
	rest   []byte
	n      int
	i      int
	ended  bool
}

func (g *genSource) Read(buf []byte) (int, error) {
	if len(g.rest) == 0 {
		if g.i >= g.n {
			if !g.ended && g.onLine != nil {
				g.onLine(g.n + 1)
			}

			g.ended = true

			return 0, io.EOF
		}

		g.i++
		if g.onLine != nil {
			g.onLine(g.i)
		}

		g.rest = []byte(fmt.Sprintf("id-%d\n", g.i))
	}

	copied := copy(buf, g.rest)
	g.rest = g.rest[copied:]

	return copied, nil
}

// importCase is one row of TestImport.
type importCase struct {
	hook           func(store *admintest.Fake, line int)
	name           string
	input          string
	normalize      resolver.Normalization
	layout         redissegment.Layout
	failOp         string
	lockedBy       string
	wantOrder      []string
	wantKeys       []string
	wantErrs       []error
	wantLive       []string
	preload        []string
	opts           segmentadmin.ImportOptions
	wantRejected   segmentadmin.Rejections
	wantAdded      int64
	wantRead       int64
	wantDuplicates int64
	generate       int
	failNth        int
	cancelLine     int
	maxID          int
	wantSADDCalls  int
	leftover       bool
	noVersionKey   bool
	noUnlink       bool
	wantSwapped    bool
	wantFellBack   bool
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
		store.Put(lastKey, deadTmp)
		_, _ = store.SAdd(ctx, deadTmp, []string{"junk"}, 0)
	}

	if tt.lockedBy != "" {
		store.Put(lockKey, tt.lockedBy)
	}

	if tt.noUnlink {
		store.WithoutUnlink()
	}

	if tt.failOp != "" {
		store.Fail(tt.failOp, tt.failNth)
	}

	var src io.Reader = strings.NewReader(tt.input)

	if tt.generate > 0 {
		src = &genSource{n: tt.generate, onLine: func(line int) {
			if line == tt.cancelLine {
				cancel()
			}

			if tt.hook != nil {
				tt.hook(store, line)
			}
		}}
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

func importCases() []importCase {
	long := strings.Repeat("x", 9)
	live := []string{liveKey}

	return []importCase{
		{
			name: "replaces the old set", input: "a\nb\na\n", preload: []string{"old1", "old2"},
			wantLive: []string{"a", "b"}, wantKeys: []string{liveKey, versionKey}, wantRead: 3, wantAdded: 2,
			wantDuplicates: 1, wantSwapped: true,
			wantOrder: []string{"SWAP " + liveKey, "SET " + versionKey, "UNLINK " + oldKey},
		},
		{
			name: "normalises as the relay does", input: " A \nb\n", normalize: resolver.NormalizeTrimLower,
			wantLive: []string{"a", "b"}, wantKeys: []string{liveKey, versionKey}, wantRead: 2, wantAdded: 2,
			wantSwapped: true,
		},
		{
			name:         "counts what it rejects",
			input:        "ok\n\n" + long + "\n\xff\nct\x01l\n",
			normalize:    resolver.NormalizeTrimLower,
			maxID:        5,
			wantLive:     []string{"ok"},
			wantKeys:     []string{liveKey, versionKey},
			wantRead:     5,
			wantAdded:    1,
			wantSwapped:  true,
			wantRejected: segmentadmin.Rejections{Empty: 1, TooLong: 1, Invalid: 2},
		},
		{
			name: "batches", input: "1\n2\n3\n4\n5\n", opts: segmentadmin.ImportOptions{BatchSize: 2},
			wantLive: []string{"1", "2", "3", "4", "5"}, wantKeys: []string{liveKey, versionKey}, wantRead: 5,
			wantAdded: 5, wantSwapped: true, wantSADDCalls: 3,
		},
		{
			name: "no version key", input: "a\n", noVersionKey: true, wantLive: []string{"a"},
			wantKeys: live, wantRead: 1, wantAdded: 1, wantSwapped: true,
		},
		{
			name: "an empty input is refused", input: "\n \n", preload: []string{"x"},
			wantErrs: []error{segmentadmin.ErrEmptyInput}, wantLive: []string{"x"}, wantKeys: live, wantRead: 2,
			wantRejected: segmentadmin.Rejections{Empty: 2},
		},
		{
			name: "an empty input is allowed on request", input: "", preload: []string{"x"},
			opts: segmentadmin.ImportOptions{AllowEmpty: true}, wantKeys: []string{versionKey}, wantSwapped: true,
		},
		{
			name: "a dry run writes nothing", input: "a\n\nb\n", preload: []string{"x"},
			opts: segmentadmin.ImportOptions{DryRun: true}, wantLive: []string{"x"}, wantKeys: live, wantRead: 3,
			wantRejected: segmentadmin.Rejections{Empty: 1},
		},
		{
			name: "a dry run refuses an empty input", input: "", opts: segmentadmin.ImportOptions{DryRun: true},
			wantErrs: []error{segmentadmin.ErrEmptyInput},
		},
		{
			name: "the keys layout is refused", input: "a\n", layout: redissegment.LayoutKeys,
			wantErrs: []error{segmentadmin.ErrKeysLayout},
		},
		{
			name: "a failed batch leaves the live set", input: "a\n", preload: []string{"x"}, failOp: opSADD,
			failNth: 1, wantErrs: []error{admintest.ErrInjected}, wantLive: []string{"x"}, wantKeys: live,
			wantRead: 1,
		},
		{
			name: "a failed second batch removes the temporary key", input: "a\nb\nc\nd\n", preload: []string{"x"},
			opts: segmentadmin.ImportOptions{BatchSize: 2}, failOp: opSADD, failNth: 2,
			wantErrs: []error{admintest.ErrInjected}, wantLive: []string{"x"}, wantKeys: live, wantRead: 4,
			wantAdded: 2,
		},
		{
			name: "a failed swap leaves the live set", input: "a\n", preload: []string{"x"}, failOp: "SWAP", failNth: 1,
			wantErrs: []error{admintest.ErrInjected}, wantLive: []string{"x"}, wantKeys: live, wantRead: 1,
			wantAdded: 1,
		},
		{
			name: "a failed version bump is reported after the swap", input: "a\n", preload: []string{"x"},
			failOp: opSET, failNth: 2, wantErrs: []error{admintest.ErrInjected, segmentadmin.ErrSwapped},
			wantLive: []string{"a"}, wantKeys: []string{liveKey, oldKey}, wantRead: 1, wantAdded: 1, wantSwapped: true,
		},
		{
			name: "a failed removal of the old set is reported", input: "a\n", preload: []string{"x"},
			failOp: opUnlink, failNth: 1, wantErrs: []error{admintest.ErrInjected, segmentadmin.ErrSwapped},
			wantLive: []string{"a"}, wantKeys: []string{liveKey, oldKey, versionKey}, wantRead: 1, wantAdded: 1,
			wantSwapped: true,
		},
		{
			name: "a cancelled run leaves the live set", preload: []string{"x"}, generate: 5, cancelLine: 3,
			opts: segmentadmin.ImportOptions{BatchSize: 1}, wantErrs: []error{context.Canceled},
			wantLive: []string{"x"}, wantKeys: live, wantRead: 3, wantAdded: 2,
		},
		{
			name: "the temporary key of a dead run is removed by its recorded name", input: "a\n", leftover: true,
			wantLive: []string{"a"}, wantKeys: []string{liveKey, versionKey}, wantRead: 1, wantAdded: 1,
			wantSwapped: true,
		},
		{
			name: "a server without UNLINK gets DEL", input: "a\n", noUnlink: true, wantLive: []string{"a"},
			wantKeys: []string{liveKey, versionKey}, wantRead: 1, wantAdded: 1, wantSwapped: true, wantFellBack: true,
		},
		{
			name: "a second import fails at once and leaves the lock of the first", input: "a\n",
			preload: []string{"x"}, lockedBy: otherToken, wantErrs: []error{segmentadmin.ErrImportRunning},
			wantLive: []string{"x"}, wantKeys: []string{liveKey, lockKey},
		},
		{
			name:     "a temporary key that expires between batches is never swapped in",
			generate: 4,
			preload:  []string{"x"},
			opts:     segmentadmin.ImportOptions{BatchSize: 1},
			wantErrs: []error{segmentadmin.ErrIncomplete},
			hook: func(store *admintest.Fake, line int) {
				if line == 3 {
					store.Delete(tmpKey)
				}
			},
			wantLive:  []string{"x"},
			wantKeys:  live,
			wantRead:  4,
			wantAdded: 4,
		},
		{
			name:     "a temporary key deleted before the swap never moves the live set",
			generate: 2,
			opts: segmentadmin.ImportOptions{
				BatchSize: 1,
			},
			preload:  []string{"x"},
			wantErrs: []error{segmentadmin.ErrTempGone},
			hook: func(store *admintest.Fake, line int) {
				if line == 3 {
					store.Delete(tmpKey)
				}
			},
			wantLive:  []string{"x"},
			wantKeys:  live,
			wantRead:  2,
			wantAdded: 2,
		},
		{
			name: "a run whose lock was taken over does not swap and leaves the other run's keys", generate: 3,
			preload: []string{"x"}, opts: segmentadmin.ImportOptions{BatchSize: 1},
			wantErrs: []error{segmentadmin.ErrLockLost},
			hook: func(store *admintest.Fake, line int) {
				if line == 2 {
					store.Put(lockKey, otherToken)
					_, _ = store.SAdd(context.Background(), otherTmp, []string{"theirs"}, 0)
				}
			},
			wantLive: []string{"x"}, wantKeys: []string{liveKey, lockKey, lastKey, otherTmp}, wantRead: 2, wantAdded: 1,
		},
		{
			name: "a lock taken over just before the swap is not swapped over", generate: 2, preload: []string{"x"},
			opts: segmentadmin.ImportOptions{BatchSize: 1}, wantErrs: []error{segmentadmin.ErrLockLost},
			hook: func(store *admintest.Fake, line int) {
				if line == 3 {
					store.Put(lockKey, otherToken)
				}
			},
			wantLive: []string{"x"}, wantKeys: []string{liveKey, lockKey, lastKey}, wantRead: 2, wantAdded: 2,
		},
	}
}

func TestImport(t *testing.T) {
	t.Parallel()

	for _, tt := range importCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			tt.opts.Token = runToken
			store, target, reader := tt.prepare(ctx, t, cancel)
			calls := len(store.Calls())

			rep, err := segmentadmin.Import(ctx, store, target, reader, tt.opts)

			for _, want := range tt.wantErrs {
				require.ErrorIs(t, err, want)
			}

			if len(tt.wantErrs) == 0 {
				require.NoError(t, err)
			}

			if tt.layout == redissegment.LayoutKeys {
				assert.Nil(t, rep)
				assert.Len(t, store.Calls(), calls, "the keys layout is refused before any command")

				return
			}

			assert.Equal(t, tt.wantRead, rep.Read)
			assert.Equal(t, tt.wantAdded, rep.Added)
			assert.Equal(t, tt.wantDuplicates, rep.Duplicates)
			assert.Equal(t, tt.wantRejected, rep.Rejected)
			assert.Equal(t, tt.wantSwapped, rep.Swapped)
			assert.Equal(t, tt.wantFellBack, rep.FellBackToDel)
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

// filterCalls keeps the calls named in want, in the order they were made.
func filterCalls(calls, want []string) []string {
	var out []string

	for _, call := range calls {
		if slices.Contains(want, call) {
			out = append(out, call)
		}
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
		t.Context(), admintest.New(), &segmentadmin.Target{Key: "{}x"}, reader, segmentadmin.ImportOptions{})
	require.ErrorIs(t, err, segmentadmin.ErrNoSlotTag)
}

func TestImport_oldSetExpires(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	_, err := store.SAdd(t.Context(), liveKey, []string{"x"}, 0)
	require.NoError(t, err)

	reader, err := segmentadmin.NewReader(strings.NewReader("a\n"), segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	store.Fail(opUnlink, 1) // the removal of the old set fails: it stays, with the ttl that bounds its life

	opts := segmentadmin.ImportOptions{TempTTL: 90 * time.Second, Token: runToken}
	_, err = segmentadmin.Import(t.Context(), store, vipTarget(), reader, opts)
	require.ErrorIs(t, err, segmentadmin.ErrSwapped)

	assert.Equal(t, 90*time.Second, store.TTLOf(oldKey))
	assert.Zero(t, store.TTLOf(liveKey), "the live set does not inherit the expiry of the temporary key")
}

// gate is an input that tells when it started and waits to be released.
type gate struct {
	started chan struct{}
	release chan struct{}
	done    bool
}

func (g *gate) Read(buf []byte) (int, error) {
	if g.done {
		return 0, io.EOF
	}

	g.done = true
	close(g.started)
	<-g.release

	return copy(buf, "winner-1\nwinner-2\n"), nil
}

func TestImport_concurrent(t *testing.T) {
	t.Parallel()

	store := admintest.New()
	first := &gate{started: make(chan struct{}), release: make(chan struct{})}

	type outcome struct {
		err error
	}

	done := make(chan outcome, 1)

	go func() {
		reader, _ := segmentadmin.NewReader(first, segmentadmin.ReaderOptions{})

		_, err := segmentadmin.Import(
			t.Context(),
			store,
			vipTarget(),
			reader,
			segmentadmin.ImportOptions{Token: "first"},
		)
		done <- outcome{err: err}
	}()

	<-first.started

	reader, err := segmentadmin.NewReader(strings.NewReader("loser-1\n"), segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	_, err = segmentadmin.Import(t.Context(), store, vipTarget(), reader, segmentadmin.ImportOptions{Token: "second"})
	require.ErrorIs(t, err, segmentadmin.ErrImportRunning)

	close(first.release)

	require.NoError(t, (<-done).err)
	assert.Equal(t, []string{"winner-1", "winner-2"}, store.Members(liveKey))
}

func TestImport_blockedInput(t *testing.T) {
	t.Parallel()

	pipeReader, pipeWriter := io.Pipe()

	t.Cleanup(func() { _ = pipeWriter.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	store := admintest.New()

	reader, err := segmentadmin.NewReader(segmentadmin.WithContext(ctx, pipeReader), segmentadmin.ReaderOptions{})
	require.NoError(t, err)

	result := make(chan error, 1)

	go func() {
		_, importErr := segmentadmin.Import(
			ctx,
			store,
			vipTarget(),
			reader,
			segmentadmin.ImportOptions{Token: runToken},
		)
		result <- importErr
	}()

	require.Eventually(t, func() bool { return len(store.Calls()) > 0 }, 10*time.Second, time.Millisecond,
		"the import did not start")
	cancel()

	select {
	case got := <-result:
		require.ErrorIs(t, got, context.Canceled)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the import did not stop with its context")
	}

	assert.Empty(t, store.Keys(), "the lock and the temporary key are gone")
}
