package segmentadmin_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

func TestSlot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want int
	}{
		{name: "the Redis specification sample", key: "123456789", want: 12739},
		{name: "foo", key: "foo", want: 12182},
		{name: "tag only is hashed", key: "a{foo}b", want: 12182},
		{name: "empty tag hashes the key", key: "{}foo", want: segmentadmin.Slot("{}foo")},
		{name: "unclosed brace hashes the key", key: "{foo", want: segmentadmin.Slot("{foo")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, segmentadmin.Slot(tt.key))
		})
	}
}

func TestTempKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr error
		name    string
		key     string
		wantTmp string
	}{
		{
			name: "default key keeps its tag", key: redissegment.DefaultKey("", "vip", redissegment.LayoutSet),
			wantTmp: "semaphore:segment:{vip}:import:new",
		},
		{name: "key without a tag is wrapped", key: "vip-list", wantTmp: "{vip-list}:import:new"},
		{name: "empty tag is wrapped", key: "{}x", wantErr: segmentadmin.ErrNoSlotTag},
		{name: "unclosed brace has no tag", key: "a}b{c", wantErr: segmentadmin.ErrNoSlotTag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tmp, old, err := segmentadmin.TempKeys(tt.key)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTmp, tmp)
			assert.Equal(t, segmentadmin.Slot(tt.key), segmentadmin.Slot(tmp))
			assert.Equal(t, segmentadmin.Slot(tt.key), segmentadmin.Slot(old))
			assert.NotEqual(t, tmp, old)
		})
	}
}

func TestScanPattern(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `a\*b\[c\]*`, segmentadmin.ScanPattern("a*b[c]"))
}

var (
	errUnknown = errors.New("ERR unknown command 'UNLINK'")
	errNoKey   = errors.New("ERR no such key")
)

func TestIsUnknownCommand(t *testing.T) {
	t.Parallel()

	assert.True(t, segmentadmin.IsUnknownCommand(errUnknown))
	assert.False(t, segmentadmin.IsUnknownCommand(errNoKey))
	assert.False(t, segmentadmin.IsUnknownCommand(nil))
}

func TestIsNoSuchKey(t *testing.T) {
	t.Parallel()

	assert.True(t, segmentadmin.IsNoSuchKey(errNoKey))
	assert.False(t, segmentadmin.IsNoSuchKey(errUnknown))
	assert.False(t, segmentadmin.IsNoSuchKey(nil))
}
