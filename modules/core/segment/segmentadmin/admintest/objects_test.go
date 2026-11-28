package admintest_test

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin/admintest"
)

func TestObjects(t *testing.T) {
	t.Parallel()

	objs := admintest.NewObjects(0)
	for _, key := range []string{"p/a", "p/b", "p/c", "p/d", "q/x"} {
		objs.Put(key, []byte(key), false)
	}

	// the listing pages by three, and the token is the last key of the page
	first, err := objs.List(t.Context(), "p/", "")
	require.NoError(t, err)
	require.Len(t, first.Objects, 3)
	assert.Equal(t, "p/c", first.Next)

	second, err := objs.List(t.Context(), "p/", first.Next)
	require.NoError(t, err)
	assert.Equal(t, []string{"p/d"}, []string{second.Objects[0].Key})
	assert.Empty(t, second.Next)

	info, err := objs.Head(t.Context(), "p/a")
	require.NoError(t, err)
	assert.Equal(t, int64(3), info.Size)

	got, err := objs.Get(t.Context(), "p/a", info.ETag)
	require.NoError(t, err)

	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	assert.Equal(t, "p/a", string(body))

	_, err = objs.Get(t.Context(), "p/a", `"other"`)
	require.ErrorIs(t, err, segmentadmin.ErrObjectChanged)

	objs.Remove("p/a")

	_, err = objs.Head(t.Context(), "p/a")
	require.ErrorIs(t, err, segmentadmin.ErrObjectMissing)

	_, err = objs.Get(t.Context(), "p/a", "")
	require.ErrorIs(t, err, segmentadmin.ErrObjectMissing)

	objs.FailHeads(2)

	_, err = objs.Head(t.Context(), "p/b")
	require.ErrorIs(t, err, admintest.ErrReadFailed)

	_, err = objs.List(t.Context(), "p/", "")
	require.ErrorIs(t, err, admintest.ErrReadFailed)

	objs.BreakAfter("p/b", 1)

	broken, err := objs.Get(t.Context(), "p/b", "")
	require.NoError(t, err)

	_, err = io.ReadAll(broken.Body)
	require.ErrorIs(t, err, admintest.ErrReadFailed)

	stopped, cancel := context.WithCancel(t.Context())
	cancel()

	_, err = objs.Get(stopped, "p/b", "")
	require.ErrorIs(t, err, context.Canceled)

	_, err = objs.Head(stopped, "p/b")
	require.ErrorIs(t, err, context.Canceled)

	assert.Equal(t, "LIST p/", objs.Calls()[0])
}
