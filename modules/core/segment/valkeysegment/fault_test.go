package valkeysegment_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// timeoutError is a network error that is a timeout.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}

var errBadByte = errors.New("unexpected byte")

// fakeClient is the seam of the provider: it builds commands with the builder of a real client, and lets a test
// replace what the client answers.
type fakeClient struct {
	inner  valkey.Client
	mutate func([]valkey.ValkeyResult) []valkey.ValkeyResult
	panics bool
}

func (f *fakeClient) B() valkey.Builder {
	if f.inner != nil {
		return f.inner.B()
	}

	return valkey.Builder{}
}

func (f *fakeClient) DoMulti(ctx context.Context, cmds ...valkey.Completed) []valkey.ValkeyResult {
	if f.panics {
		panic("client bug")
	}

	if f.inner == nil {
		return nil
	}

	results := f.inner.DoMulti(ctx, cmds...)
	if f.mutate != nil {
		return f.mutate(results)
	}

	return results
}

func faulty(tb testing.TB, srv *stubServer, mutate func([]valkey.ValkeyResult) []valkey.ValkeyResult) *fakeClient {
	tb.Helper()

	client, err := valkey.NewClient(*srv.clientOption())
	require.NoError(tb, err)
	tb.Cleanup(client.Close)

	return &fakeClient{inner: client, mutate: mutate}
}

// replace answers the result at pos with an error.
func replace(pos int, err error) func([]valkey.ValkeyResult) []valkey.ValkeyResult {
	return func(results []valkey.ValkeyResult) []valkey.ValkeyResult {
		results[pos] = valkey.NewErrorResult(err)

		return results
	}
}

func TestContainsKeepsWhatWasAnswered(t *testing.T) {
	t.Parallel()

	// commands: 0 SMISMEMBER beta, 1 EXISTS beta, 2 EXISTS flag:alice
	lookups := func() []segment.Lookup {
		return []segment.Lookup{lk(segBeta, idAlice), lk(segFlags, idAlice), lk(segBeta, "zed")}
	}

	tests := []struct {
		name       string
		mutate     func([]valkey.ValkeyResult) []valkey.ValkeyResult
		wantStates []segment.State
		wantClass  valkeysegment.Class
		wantFailed int
	}{
		{
			name:       "a transport error keeps the commands that were read",
			mutate:     replace(2, io.EOF),
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.NotMember},
			wantClass:  valkeysegment.ClassUnavailable,
			wantFailed: 1,
		},
		{
			name:       "a reset keeps the commands that were read",
			mutate:     replace(2, net.ErrClosed),
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.NotMember},
			wantClass:  valkeysegment.ClassUnavailable,
			wantFailed: 1,
		},
		{
			name:       "a closing client is unavailable",
			mutate:     replace(0, valkey.ErrClosing),
			wantStates: []segment.State{segment.Unknown, segment.Member, segment.Unknown},
			wantClass:  valkeysegment.ClassUnavailable,
			wantFailed: 2,
		},
		{
			name:       "a network timeout is a timeout",
			mutate:     replace(2, timeoutError{}),
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.NotMember},
			wantClass:  valkeysegment.ClassTimeout,
			wantFailed: 1,
		},
		{
			name:       "an expired deadline is a timeout",
			mutate:     replace(2, context.DeadlineExceeded),
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.NotMember},
			wantClass:  valkeysegment.ClassTimeout,
			wantFailed: 1,
		},
		{
			name:       "an extra result is not trusted",
			mutate:     func(results []valkey.ValkeyResult) []valkey.ValkeyResult { return append(results, results[0]) },
			wantStates: []segment.State{segment.Unknown, segment.Unknown, segment.Unknown},
			wantClass:  valkeysegment.ClassProtocol,
			wantFailed: 3,
		},
		{
			name:       "a cancel is a cancel",
			mutate:     replace(2, context.Canceled),
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.NotMember},
			wantClass:  valkeysegment.ClassCanceled,
			wantFailed: 1,
		},
		{
			name:       "a failed key check leaves the non-members unknown and keeps the members",
			mutate:     replace(1, io.EOF),
			wantStates: []segment.State{segment.Member, segment.Member, segment.Unknown},
			wantClass:  valkeysegment.ClassUnavailable,
			wantFailed: 1,
		},
		{
			name:       "a parse error anywhere discards every answer",
			mutate:     replace(2, errBadByte),
			wantStates: []segment.State{segment.Unknown, segment.Unknown, segment.Unknown},
			wantClass:  valkeysegment.ClassProtocol,
			wantFailed: 3,
		},
		{
			name:       "a parse error in an early command discards the later answers",
			mutate:     replace(0, errBadByte),
			wantStates: []segment.State{segment.Unknown, segment.Unknown, segment.Unknown},
			wantClass:  valkeysegment.ClassProtocol,
			wantFailed: 3,
		},
		{
			name:       "too few results discard every answer",
			mutate:     func(results []valkey.ValkeyResult) []valkey.ValkeyResult { return results[:2] },
			wantStates: []segment.State{segment.Unknown, segment.Unknown, segment.Unknown},
			wantClass:  valkeysegment.ClassProtocol,
			wantFailed: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			counters := &valkeysegment.Counters{}
			opts := baseOptions(counters)

			prov, err := valkeysegment.New(faulty(t, srv, tt.mutate), opts)
			require.NoError(t, err)

			calls := lookups()
			err = prov.Contains(context.Background(), calls)

			var lerr *valkeysegment.LookupError

			require.ErrorAs(t, err, &lerr)
			assert.Equal(t, &valkeysegment.LookupError{Class: tt.wantClass, Failed: tt.wantFailed, Total: 3}, lerr)
			assert.Equal(t, tt.wantStates, states(calls))
			assert.Equal(t, map[string]uint64{tt.wantClass.String(): 1}, counters.Snapshot().Failures)
		})
	}
}

func TestClientPanicIsRecovered(t *testing.T) {
	t.Parallel()

	client := faulty(t, loaded(t), nil)
	client.panics = true

	prov, err := valkeysegment.New(client, baseOptions(nil))
	require.NoError(t, err)

	lookups := []segment.Lookup{{Segment: segBeta, ID: idAlice, State: segment.Member}, lk(segFlags, idAlice)}
	err = prov.Contains(context.Background(), lookups)

	assert.Equal(t, &valkeysegment.LookupError{Class: valkeysegment.ClassUnavailable, Failed: 2, Total: 2}, err)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, states(lookups))

	got, err := prov.Exists(context.Background(), []string{segBeta})
	assert.Equal(t, &valkeysegment.LookupError{Class: valkeysegment.ClassUnavailable, Failed: 1, Total: 1}, err)
	assert.Equal(t, []segment.State{segment.Unknown}, got)

	initErr := prov.Init(context.Background())
	require.ErrorIs(t, initErr, valkeysegment.ErrInit)
	require.ErrorIs(t, initErr, valkeysegment.ErrUnavailable)
}

func TestExistsAndInitWithShortResults(t *testing.T) {
	t.Parallel()

	short := func(results []valkey.ValkeyResult) []valkey.ValkeyResult { return results[:1] }

	srv := loaded(t)
	prov, err := valkeysegment.New(faulty(t, srv, short), baseOptions(nil))
	require.NoError(t, err)

	got, err := prov.Exists(context.Background(), []string{segBeta, segGhost})
	assert.Equal(t, &valkeysegment.LookupError{Class: valkeysegment.ClassProtocol, Failed: 2, Total: 2}, err)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, got)

	initErr := prov.Init(context.Background())
	require.ErrorIs(t, initErr, valkeysegment.ErrProtocol)

	malformed, err := valkeysegment.New(faulty(t, srv, replace(1, errBadByte)), baseOptions(nil))
	require.NoError(t, err)

	got, err = malformed.Exists(context.Background(), []string{segBeta, segGhost})
	assert.Equal(t, &valkeysegment.LookupError{Class: valkeysegment.ClassProtocol, Failed: 2, Total: 2}, err)
	assert.Equal(t, []segment.State{segment.Unknown, segment.Unknown}, got)
}

func TestAnErrorAfterTheCallTimeoutIsATimeout(t *testing.T) {
	t.Parallel()

	opts := baseOptions(nil)
	opts.Timeout = time.Nanosecond // the call context has expired when the client answers

	prov, err := valkeysegment.New(faulty(t, loaded(t), replace(0, io.EOF)), opts)
	require.NoError(t, err)

	lookups := []segment.Lookup{lk(segFlags, idAlice)}
	err = prov.Contains(context.Background(), lookups)

	assert.Equal(t, &valkeysegment.LookupError{Class: valkeysegment.ClassTimeout, Failed: 1, Total: 1}, err)
	assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
}
