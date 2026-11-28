package snapshot_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/snapshot"
)

var errAnswerStore = errors.New("the store is down")

const answerMember = "alice"

func neverLoaded(t *testing.T) *snapshot.Holder {
	t.Helper()

	holder, err := snapshot.New(segAlpha, segment.NewMemorySource(nil), snapshot.Options{})
	require.NoError(t, err)

	return holder
}

func loadedHolder(t *testing.T) *snapshot.Holder {
	t.Helper()

	holder, _ := loaded(t, answerMember)

	return holder
}

func staleHolder(t *testing.T) *snapshot.Holder {
	t.Helper()

	holder, src := loaded(t, answerMember)
	src.Fail(segment.OpVersion, errAnswerStore)
	require.Error(t, holder.Refresh(context.Background()))

	return holder
}

func missingHolder(t *testing.T) *snapshot.Holder {
	t.Helper()

	holder, src := loaded(t, answerMember)
	src.Delete(segAlpha)
	require.Error(t, holder.Refresh(context.Background()))

	return holder
}

func missingUnknownHolder(t *testing.T) *snapshot.Holder {
	t.Helper()

	holder, err := snapshot.New(
		segAlpha,
		segment.NewMemorySource(nil),
		snapshot.Options{Missing: segment.MissingUnknown},
	)
	require.NoError(t, err)
	require.Error(t, holder.Refresh(context.Background()))

	return holder
}

func failedFirstLoad(t *testing.T) *snapshot.Holder {
	t.Helper()

	src := segment.NewMemorySource(nil)
	src.Fail(segment.OpVersion, errAnswerStore)

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
	require.NoError(t, err)
	require.Error(t, holder.Refresh(context.Background()))

	return holder
}

func missingUnknownThenFails(t *testing.T) *snapshot.Holder {
	t.Helper()

	src := segment.NewMemorySource(nil)

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{Missing: segment.MissingUnknown})
	require.NoError(t, err)
	require.Error(t, holder.Refresh(context.Background()))

	src.Fail(segment.OpVersion, errAnswerStore)
	require.Error(t, holder.Refresh(context.Background()))

	return holder
}

func overdueHolder(t *testing.T) *snapshot.Holder {
	t.Helper()

	holder := loadedHolder(t)
	holder.SetOverdue(true)

	return holder
}

func missingNotMemberThenFails(t *testing.T) *snapshot.Holder {
	t.Helper()

	src := segment.NewMemorySource(nil)

	holder, err := snapshot.New(segAlpha, src, snapshot.Options{})
	require.NoError(t, err)
	require.Error(t, holder.Refresh(context.Background()))

	src.Fail(segment.OpVersion, errAnswerStore)
	require.Error(t, holder.Refresh(context.Background()))

	return holder
}

func TestHolderAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup      func(t *testing.T) *snapshot.Holder
		name       string
		id         string
		wantState  segment.State
		wantStatus segment.Status
	}{
		{
			name: "never loaded", setup: neverLoaded, id: answerMember,
			wantState: segment.Unknown, wantStatus: segment.StatusStoreError,
		},
		{
			name: "first load failed", setup: failedFirstLoad, id: answerMember,
			wantState: segment.Unknown, wantStatus: segment.StatusStoreError,
		},
		{
			name: "member", setup: loadedHolder, id: answerMember,
			wantState: segment.Member, wantStatus: segment.StatusHealthy,
		},
		{
			name: "not a member", setup: loadedHolder, id: "bob",
			wantState: segment.NotMember, wantStatus: segment.StatusHealthy,
		},
		{
			name: "stale after an error", setup: staleHolder, id: answerMember,
			wantState: segment.Member, wantStatus: segment.StatusStale,
		},
		{
			name: "missing under notMember, then the store fails", setup: missingNotMemberThenFails, id: answerMember,
			wantState: segment.NotMember, wantStatus: segment.StatusStale,
		},
		{
			name: "overdue", setup: overdueHolder, id: answerMember,
			wantState: segment.Member, wantStatus: segment.StatusStale,
		},
		{
			name: "missing under unknown, then the store fails", setup: missingUnknownThenFails, id: answerMember,
			wantState: segment.Unknown, wantStatus: segment.StatusStoreError,
		},
		{
			name: "missing under unknown is an answer", setup: missingUnknownHolder, id: answerMember,
			wantState: segment.Unknown, wantStatus: segment.StatusHealthy,
		},
		{
			name: "missing is an answer", setup: missingHolder, id: answerMember,
			wantState: segment.NotMember, wantStatus: segment.StatusHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state, status := tt.setup(t).Answer(tt.id)

			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantStatus, status)
		})
	}
}

// An error in which the store answered about the set (a key of the wrong type) is the class of the set, not a read
// failure: the consumer tells a broken set from a store that fails.
func TestHolderClassOfAnAnsweredError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err       error
		name      string
		wantClass snapshot.Class
	}{
		{
			name:      "the store answered",
			err:       fmt.Errorf("scan: %w", answeredError("wrong type")),
			wantClass: snapshot.ClassSet,
		},
		{name: "the store failed", err: errAnswerStore, wantClass: snapshot.ClassRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			holder, src := loaded(t, answerMember)
			src.Set(segAlpha, []string{answerMember, "bob"}) // a new version: the members are pulled
			src.Fail(segment.OpMembers, tt.err)

			require.Error(t, holder.Refresh(context.Background()))
			assert.Equal(t, tt.wantClass, holder.Status().LastError)
			assert.True(t, holder.Stale())
		})
	}
}

// answeredError is a failure of a store that answered about one set.
type answeredError string

func (e answeredError) Error() string { return string(e) }

func (answeredError) StoreAnswered() bool { return true }
