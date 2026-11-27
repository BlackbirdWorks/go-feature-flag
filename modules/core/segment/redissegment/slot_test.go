package redissegment_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// slotOf is the cluster slot of a key: CRC16 (XMODEM) of the hash tag when the key has one, modulo 16384.
func slotOf(key string) uint16 {
	if start := strings.IndexByte(key, '{'); start >= 0 {
		if end := strings.IndexByte(key[start+1:], '}'); end > 0 {
			key = key[start+1 : start+1+end]
		}
	}

	var crc uint16

	for i := range len(key) {
		crc ^= uint16(key[i]) << 8

		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}

	return crc % 16384
}

func TestSlotOfMatchesTheReferenceValues(t *testing.T) {
	t.Parallel()

	// the reference value of the Redis cluster specification: CRC16("123456789") is 0x31C3
	assert.Equal(t, uint16(0x31C3%16384), slotOf("123456789"))
	assert.Equal(t, slotOf("beta"), slotOf("x{beta}y"), "the hash tag decides")
}

// generatedKey returns the key that the provider generates for a set segment: the one it sends in EXISTS.
func generatedKey(t *testing.T, name string) string {
	t.Helper()

	store := &fakeStore{}
	prov := newProvider(t, store, &redissegment.Options{MissingKey: redissegment.MissingUnknown,
		Segments: map[string]redissegment.SegmentOptions{name: {}},
	})

	_, err := prov.Exists(boundedCtx(t), []string{name})
	require.NoError(t, err)

	cmds := store.commands()
	require.Len(t, cmds, 1)
	require.Len(t, cmds[0], 1)

	key, found := strings.CutPrefix(cmds[0][0], "exists ")
	require.True(t, found, cmds[0][0])

	return key
}

// TestTemporaryKeyIsInTheSlotOfTheSet pins the cluster rule of the swap: the temporary key of a set that is
// replaced with RENAME must be in the slot of the live key, which is what the hash tag of the generated key does.
func TestTemporaryKeyIsInTheSlotOfTheSet(t *testing.T) {
	t.Parallel()

	live := generatedKey(t, "beta")
	other := generatedKey(t, "alpha")

	require.Equal(t, "semaphore:segment:{beta}", live, "the key is generated with the name as the hash tag")

	tests := []struct {
		name     string
		temp     string
		wantSame bool
	}{
		{name: "the generated key and a temporary key with the same tag", temp: live + ":tmp", wantSame: true},
		{name: "another suffix with the same tag", temp: live + ":import:7", wantSame: true},
		{
			name:     "a temporary key without the tag lands elsewhere",
			temp:     strings.NewReplacer("{", "", "}", "").Replace(live) + ":tmp",
			wantSame: false,
		},
		{name: "two segments are in different slots", temp: other, wantSame: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.wantSame, slotOf(live) == slotOf(tt.temp))
		})
	}
}
