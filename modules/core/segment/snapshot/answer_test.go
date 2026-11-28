package snapshot_test

import (
	"context"
	"errors"
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
