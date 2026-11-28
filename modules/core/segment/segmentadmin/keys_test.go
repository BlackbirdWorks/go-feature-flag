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

func TestCheckKeys(t *testing.T) {
	t.Parallel()

	set := func(name, key, version string) *segmentadmin.Target {
		return &segmentadmin.Target{Name: name, Key: key, VersionKey: version}
	}

	tests := []struct {
		a       *segmentadmin.Target
		b       *segmentadmin.Target
		name    string
		wantErr bool
	}{
		{name: "distinct segments", a: set("a", "p:{a}", "p:a:v"), b: set("b", "p:{b}", "p:b:v")},
		{name: "the same key", a: set("a", "p:{a}", ""), b: set("b", "p:{a}", ""), wantErr: true},
		{
			name:    "a key is the lock of the other",
			a:       set("a", "p:{a}", ""),
			b:       set("b", "p:{a}:import:lock", ""),
			wantErr: true,
		},
		{
			name:    "a key is the temporary key of the other",
			a:       set("a", "p:{a}", ""),
			b:       set("b", "p:{a}:import:tok:new", ""),
			wantErr: true,
		},
		{
			name:    "a version key is under the import keys of the other",
			a:       set("a", "p:{a}", ""),
			b:       set("b", "p:{b}", "p:{a}:import:last"),
			wantErr: true,
		},
		{
			name:    "a key without a tag is wrapped",
			a:       set("a", "plain", ""),
			b:       set("b", "{plain}:import:lock", ""),
			wantErr: true,
		},
		{name: "a key that only starts like the other", a: set("a", "p:{a}", ""), b: set("b", "p:{a}x", "")},
		{
			name: "member keys that can be the derived keys", a: set("a", "p:{a}", ""),
			b: &segmentadmin.Target{Name: "b", Key: "p:{a}:", Layout: redissegment.LayoutKeys}, wantErr: true,
		},
		{
			name: "no import of the keys layout: nothing is derived",
			a: &segmentadmin.Target{
				Name:   "a",
				Key:    "p:a:",
				Layout: redissegment.LayoutKeys,
			},
			b: set("b", "p:a:import:lock", ""),
		},
		{name: "a segment without a key", a: set("a", "", ""), b: set("b", "p:{b}", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, pair := range [][2]*segmentadmin.Target{{tt.a, tt.b}, {tt.b, tt.a}} {
				err := segmentadmin.CheckKeys(pair[0], pair[1])
				if tt.wantErr {
					require.ErrorIs(t, err, segmentadmin.ErrKeyOverlap)
				} else {
					require.NoError(t, err)
				}
			}
		})
	}
}
