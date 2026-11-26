package manifestreader_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
)

const (
	nameAlpha = "alpha"
	nameBeta  = "beta"
	nameGamma = "gamma"
	nameTwin  = "twin"
	rawGet    = "x-id=GetObject"
	// anyChannel makes the rig leave the expected channel empty.
	anyChannel    = "*"
	otherChannel  = "canary"
	connElsewhere = "elsewhere"
	queryVersion  = "versionId=v-1"

	contentAlphaChanged = `{"alpha":"changed"}`
	contentAlphaSecond  = `{"alpha":"second"}`

	keyPrefix   = "tenant/a/" // the publisher applies the prefix; the reader fetches keys verbatim
	manifestKey = keyPrefix + "manifests/stable.yaml"
	testChannel = "stable"
	connControl = "control"
)

func hexSum(content []byte) []byte {
	sum := sha256.Sum256(content)

	return []byte(hex.EncodeToString(sum[:]))
}

// obj is a source object of a test manifest.
type obj struct {
	name    string
	content string
}

// entryOf is the manifest entry of item, with the content-addressed key of the publisher.
func entryOf(tb testing.TB, item obj) controlplane.ManifestObject {
	tb.Helper()

	sum := controlplane.ChecksumOf([]byte(item.content))
	key, err := controlplane.ObjectKey(sum, "json")
	require.NoError(tb, err)

	key = keyPrefix + key

	return controlplane.ManifestObject{
		Name:       item.name,
		Kind:       controlplane.KindFlags,
		Connection: connControl,
		Key:        key,
		Format:     controlplane.FormatJSON,
		Checksum:   sum,
		Size:       int64(len(item.content)),
	}
}

func keyOf(tb testing.TB, o obj) string {
	tb.Helper()

	return entryOf(tb, o).Key
}

// manifestOf is the manifest of generation gen listing objs.
func manifestOf(tb testing.TB, gen uint64, objs ...obj) *controlplane.Manifest {
	tb.Helper()

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: testChannel,
		Generation: gen, Objects: make([]controlplane.ManifestObject, 0, len(objs)),
	}

	for _, o := range objs {
		manifest.Objects = append(manifest.Objects, entryOf(tb, o))
	}

	return manifest
}

// publish writes the objects, then the manifest, as a publisher does.
func publish(tb testing.TB, store *fakeStore, gen uint64, objs ...obj) *controlplane.Manifest {
	tb.Helper()

	for _, o := range objs {
		store.put(keyOf(tb, o), []byte(o.content))
	}

	manifest := manifestOf(tb, gen, objs...)
	putManifest(tb, store, manifest)

	return manifest
}

func putManifest(tb testing.TB, store *fakeStore, manifest *controlplane.Manifest) {
	tb.Helper()

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(tb, err)

	store.put(manifestKey, data)
}

// manifestETag is the ETag the fake store gives the manifest it holds now.
func manifestETag(store *fakeStore) string {
	store.mu.Lock()
	defer store.mu.Unlock()

	return store.etags[manifestKey]
}

// manifestCall is the call the reader makes for the manifest when it holds etag.
func manifestCall(etag string) call { return call{Key: manifestKey, ETag: etag} }

func objCall(tb testing.TB, item obj) call {
	tb.Helper()

	return call{Key: keyOf(tb, item)}
}

// coldCalls is what a reader that holds nothing requests for a manifest whose objects to fetch are objs: the
// manifest unconditionally, then each object once, in key order.
func coldCalls(tb testing.TB, objs ...obj) []call {
	tb.Helper()

	calls := make([]call, 0, 1+len(objs))
	calls = append(calls, manifestCall(""))

	for _, item := range objs {
		calls = append(calls, objCall(tb, item))
	}

	return sortCalls(calls)
}

// threeObjects returns the three objects most scenarios start from.
func threeObjects() []obj {
	return []obj{
		{name: nameAlpha, content: `{"alpha":1}`},
		{name: nameBeta, content: `{"beta":2}`},
		{name: nameGamma, content: `{"gamma":3}`},
	}
}

// clocktestInstant is the instant the clock is pinned to for this package.
func clocktestInstant() time.Time { return clocktest.Instant() }

func mustMarshal(tb testing.TB, manifest *controlplane.Manifest) []byte {
	tb.Helper()

	data, err := controlplane.MarshalManifest(manifest)
	require.NoError(tb, err)

	return data
}

func replaceOnce(data []byte, old, replacement string) []byte {
	return bytes.Replace(data, []byte(old), []byte(replacement), 1)
}

var (
	errCause        = errors.New("the cause")
	errTransient    = errors.New("boom")
	errDenied       = errors.New("nope")
	errUnclassified = errors.New("unclassified")
)
