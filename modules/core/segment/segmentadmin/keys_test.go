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

	// the values are those of CLUSTER KEYSLOT, written here and not computed by the code under test
	tests := []struct {
		name string
		key  string
		want int
	}{
		{name: "the Redis specification sample", key: "123456789", want: 12739},
		{name: "foo", key: "foo", want: 12182},
		{name: "bar", key: "bar", want: 5061},
		{name: "hello", key: "hello", want: 866},
		{name: "somekey", key: "somekey", want: 11058},
		{name: "tag only is hashed", key: "a{foo}b", want: 12182},
		{name: "the first tag counts", key: "{foo}{bar}", want: 12182},
		{name: "empty tag hashes the whole key", key: "foo{}{bar}", want: 8363},
		{name: "unclosed brace hashes the whole key", key: "{foo", want: 13308},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, segmentadmin.Slot(tt.key))
		})
	}
}

func TestSlot_sharedTag(t *testing.T) {
	t.Parallel()

	// the example of the Redis cluster specification
	assert.Equal(t, segmentadmin.Slot("{user1000}.following"), segmentadmin.Slot("{user1000}.followers"))
	assert.NotEqual(t, segmentadmin.Slot("user1000.following"), segmentadmin.Slot("user1000.followers"))
}

func TestImportKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantErr  error
		name     string
		key      string
		wantTmp  string
		wantLock string
	}{
		{
			name: "default key keeps its tag", key: redissegment.DefaultKey("", "vip", redissegment.LayoutSet),
			wantTmp: "semaphore:segment:{vip}:import:tok:new", wantLock: "semaphore:segment:{vip}:import:lock",
		},
		{
			name: "key without a tag is wrapped", key: "vip-list", wantTmp: "{vip-list}:import:tok:new",
			wantLock: "{vip-list}:import:lock",
		},
		{name: "empty tag cannot be shared", key: "{}x", wantErr: segmentadmin.ErrNoSlotTag},
		{name: "unclosed brace cannot be shared", key: "a}b{c", wantErr: segmentadmin.ErrNoSlotTag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys, err := segmentadmin.ImportKeys(tt.key, "tok")
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTmp, keys.Tmp)
			assert.Equal(t, tt.wantLock, keys.Lock)

			for _, key := range []string{keys.Tmp, keys.Old, keys.Lock, keys.Last} {
				assert.Equal(t, segmentadmin.Slot(tt.key), segmentadmin.Slot(key), key)
			}

			assert.True(t, keys.Owns(keys.Tmp))
			assert.False(t, keys.Owns("other:key"))

			again, err := segmentadmin.ImportKeys(tt.key, "other")
			require.NoError(t, err)
			assert.NotEqual(t, keys.Tmp, again.Tmp, "the temporary key is the run's own")
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
