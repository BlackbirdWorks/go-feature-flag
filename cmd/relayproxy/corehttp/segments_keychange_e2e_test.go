//go:build docker

package corehttp_test

import (
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	keychangeVersion = "    version: {key: aud-version, interval: 100ms}\n"
	snapshotSegMoved = segHeaderYAML + "  - name: beta\n    members: [alice]\n" +
		"  - name: audience\n    connection: audiences\n    key: aud-v2\n" + keychangeVersion
)

func gzipText(tb testing.TB, text string) []byte {
	tb.Helper()

	var buf bytes.Buffer

	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(text))
	require.NoError(tb, err)
	require.NoError(tb, zw.Close())

	return buf.Bytes()
}

func isNotMember(b evalBody) bool { return !b.Value }

// Publishing the definition with another key replaces the segment: no version bump, no version key written. The new
// list answers after the reload, and the old one no longer does. A snapshot segment pulls the new set at once, and a
// lookup segment does not serve the answers that it cached for the old key.
func TestE2E_SegmentKeyChangeServesTheNewList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		old   string
		moved string
	}{
		{name: "snapshot", old: snapshotSegDoc, moved: snapshotSegMoved},
		{name: "lookup", old: valkeySegDoc, moved: valkeySegDocMoved},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := startValkey(t)
			rig := valkeyRig(t, store.address)

			store.add(t, "aud", "alice")
			store.add(t, "aud-v2", "bob")
			store.add(t, "aud-moved", "bob")
			store.setVersion(t, "1")

			rig.segments[segObjectName] = []byte(tt.old)
			rig.publish(t)
			rig.poll(t)

			awaitEval(t, rig, "alice", isMember, "a member of the old list is targeted (and cached)")
			awaitEval(t, rig, "bob", isNotMember, "a member of the new list only is not (and cached)")

			rig.segments[segObjectName] = []byte(tt.moved)
			rig.publish(t)
			rig.poll(t)

			awaitEval(t, rig, "bob", isMember, "a member of the new list is targeted after the reload")
			awaitEval(t, rig, "alice", isNotMember, "a member of the old list only is not")
		})
	}
}
