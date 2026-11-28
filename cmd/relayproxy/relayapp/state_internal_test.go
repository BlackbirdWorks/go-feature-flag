package relayapp

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// The state directory holds what the engine applied. A generation that the engine rejects does not replace the last
// good one on disk: after a restart with the store down, the relay serves the good generation with its values.
func TestRunRestartsOnTheLastAppliedGenerationWhenTheStoreIsDown(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := startRelay(t, &rigOptions{
		extra: "auth:\n  adminKeys: [" + adminKey + "]\n", flags: []string{"--state-dir=" + stateDir},
	})
	first.awaitGeneration(1)

	// generation 2 is applied: the flag pub is public with the value 7
	first.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7),
	})
	first.poll()

	// generation 3 is read by the reader and rejected by the engine: two public flag sets define the flag pub
	first.store.publishDocs(publicSets(openSet, "other"), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7), otherObject: flagYAMLOf(publicFlag, 8),
	})
	first.pollRejected(t, 3, flagruntime.CodeBuildFailed)

	first.cancel()
	assert.Equal(t, bootstrap.DefaultShutdownTimeout, first.awaitTimeout())
	assert.Equal(t, 0, first.awaitCode(), "the relay stopped after it wrote the state")

	// the store is down for the second start: its manifest is not there
	second := startRelay(t, &rigOptions{
		extra: "auth:\n  adminKeys: [" + adminKey + "]\n", manifestKey: missingManifest,
		flags: []string{"--state-dir=" + stateDir},
	})
	second.awaitGeneration(2)

	status, body := doRequest(t, http.MethodPost, second.url+"/v1/feature/pub/eval", evalBody, jsonHeader())
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `"value":7`, "the public flag of the applied generation, with its value")

	status, value := second.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 1, value, 0, "the flag of the flag set with a key")
}

// missingManifest is a manifest key that the store does not have: the store is down for the relay.
const missingManifest = "manifests/missing.yaml"

var errDiskGone = errors.New("the disk is gone")

// brokenFS is a state directory file system whose writes never finish: every rename fails. The second open is the
// one of the write that follows a failed write, so it tells that the status of the first failure is recorded.
type brokenFS struct {
	statedir.FS

	reopened chan struct{}
	opens    int
	mu       sync.Mutex
}

//nolint:ireturn // implements the FS seam, which returns an interface
func (b *brokenFS) Open(dir string) (statedir.Root, error) {
	root, err := b.FS.Open(dir)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	b.opens++

	if b.opens == 2 {
		close(b.reopened)
	}

	b.mu.Unlock()

	return &brokenRoot{Root: root}, nil
}

type brokenRoot struct{ statedir.Root }

func (*brokenRoot) Rename(string, string) error { return errDiskGone }

// A state directory that cannot be written never stops the relay: it serves, the failure is counted and /info says it.
func TestRunServesWhenTheStateDirectoryCannotBeWritten(t *testing.T) {
	t.Parallel()

	broken := &brokenFS{FS: statedir.NewOSFS(), reopened: make(chan struct{})}
	rig := startRelay(t, &rigOptions{
		stateFS: broken, extra: "auth:\n  adminKeys: [" + adminKey + "]\n",
		flags: []string{"--state-dir=" + filepath.Join(t.TempDir(), "state")},
	})
	rig.awaitGeneration(1)

	// the next applied generation makes the writer try again, after it recorded the failure of the first
	rig.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7),
	})
	rig.poll()

	select {
	case <-broken.reopened:
	case <-time.After(waitBound):
		require.FailNow(t, "the state writer did not try the next write", rig.stderr.String())
	}

	status, value := rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status, "the relay serves")
	assert.InDelta(t, 1, value, 0)

	status, body := get(t, rig.url+infoPath, http.Header{headerAccept: {mimeJSON}})
	require.Equal(t, http.StatusOK, status, body)

	var info struct {
		Config struct {
			State model.StateInfo `json:"state"`
		} `json:"config"`
	}

	require.NoError(t, json.Unmarshal([]byte(body), &info))
	assert.True(t, info.Config.State.Enabled)
	assert.Equal(t, statedir.ReasonIO, info.Config.State.LastSaveError)
	assert.GreaterOrEqual(t, info.Config.State.SaveErrors, uint64(1))

	_, metrics := get(t, rig.url+"/metrics", nil)
	assert.Regexp(t, `semaphore_manifest_state_saves_total\{connection="control",result="error"\} [1-9]`, metrics)
}

// removeObject deletes the object of a content from the store: the manifest still lists it, and the download fails.
func (s *store) removeObject(content string) {
	s.t.Helper()

	key, err := controlplane.ObjectKey(controlplane.ChecksumOf([]byte(content)), controlplane.FormatYAML)
	require.NoError(s.t, err)
	require.NoError(s.t, os.Remove(filepath.Join(s.dir, key)))
}

// After a rejected generation, a later generation whose object cannot be downloaded is served from the content the
// relay runs: the state directory holds that content, not the bytes the reader fetched for the rejected generation.
func TestRunRestartsOnTheContentTheRuntimeHeld(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := startRelay(t, &rigOptions{flags: []string{"--state-dir=" + stateDir}})
	first.awaitGeneration(1)

	// generation 2: the public flag pub is 7
	first.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 7),
	})
	first.poll()

	// generation 3 is rejected (the flag pub is in two public flag sets); the reader keeps the bytes of pub valued 8
	first.store.publishDocs(publicSets(openSet, "other"), map[string]string{
		flagsObject: flagYAML(1), openObject: flagYAMLOf(publicFlag, 8), otherObject: flagYAMLOf(publicFlag, 9),
	})
	first.pollRejected(t, 3, flagruntime.CodeBuildFailed)

	// generation 4: the conflict is gone, and the object of pub valued 10 cannot be downloaded: the relay keeps serving 7
	first.store.publishDocs(publicSets(openSet), map[string]string{
		flagsObject: flagYAML(2), openObject: flagYAMLOf(publicFlag, 10),
	})
	first.store.removeObject(flagYAMLOf(publicFlag, 10))
	first.poll()

	status, body := doRequest(t, http.MethodPost, first.url+"/v1/feature/pub/eval", evalBody, jsonHeader())
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, body, `"value":7`, "the running relay serves the content it holds")

	first.cancel()
	assert.Equal(t, bootstrap.DefaultShutdownTimeout, first.awaitTimeout())
	assert.Equal(t, 0, first.awaitCode())

	second := startRelay(t, &rigOptions{
		manifestKey: missingManifest, flags: []string{"--state-dir=" + stateDir},
	})
	second.awaitGeneration(4)

	status, body = doRequest(t, http.MethodPost, second.url+"/v1/feature/pub/eval", evalBody, jsonHeader())
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(
		t,
		body,
		`"value":7`,
		"the restart serves what the relay served, not the bytes of the rejected generation",
	)

	status, value := second.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 2, value, 0, "the flags object that was downloaded")
}

// A flag that fails validation keeps serving its earlier definition in the running relay, and /info counts it. The
// state directory holds the bytes of the object, not the earlier definition: a restart serves no such flag.
func TestRunRestartLosesTheFlagsServedFromAnEarlierVersion(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := startRelay(t, &rigOptions{flags: []string{"--state-dir=" + stateDir}})
	first.awaitGeneration(1)

	// generation 2: the flag f has a variation that does not exist, so the definition of generation 1 stays
	first.store.publishDocs(publicSets(), map[string]string{
		flagsObject: "f:\n  variations:\n    v: 5\n  defaultRule:\n    variation: nope\n",
	})
	first.poll()

	status, value := first.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 1, value, 0, "the relay serves the earlier definition")

	status, body := get(t, first.url+infoPath, http.Header{headerAccept: {mimeJSON}})
	require.Equal(t, http.StatusOK, status)

	var info struct {
		Config struct {
			Objects []model.ObjectInfo `json:"objects"`
			State   model.StateInfo    `json:"state"`
		} `json:"config"`
	}

	require.NoError(t, json.Unmarshal([]byte(body), &info))
	assert.Equal(t, 1, info.Config.State.KeptFlags)

	var kept []string

	for _, obj := range info.Config.Objects {
		if obj.Kept > 0 {
			kept = append(kept, obj.Name)
		}
	}

	assert.Equal(t, []string{flagsObject}, kept)

	first.cancel()
	assert.Equal(t, bootstrap.DefaultShutdownTimeout, first.awaitTimeout())
	assert.Equal(t, 0, first.awaitCode())

	second := startRelay(t, &rigOptions{
		manifestKey: missingManifest, flags: []string{"--state-dir=" + stateDir},
	})
	second.awaitGeneration(2)

	status, _ = second.evaluate(tenantKey)
	assert.Equal(t, http.StatusNotFound, status, "the restart has the object, and the flag in it is rejected")
}

// The write of the state directory is off the path of the swap: with the disk held in the middle of a write, the next
// generation is applied and served all the same.
func TestRunAppliesGenerationsWhileTheStateWriteIsHeld(t *testing.T) {
	t.Parallel()

	held := &heldFS{
		FS: statedir.NewOSFS(), entered: make(chan struct{}), release: make(chan struct{}),
		closed: make(chan struct{}),
	}
	rig := startRelay(t, &rigOptions{
		stateFS: held, flags: []string{"--state-dir=" + filepath.Join(t.TempDir(), "state")},
	})

	// registered after the rig: it runs first, the held write ends and the writer is joined before the directory goes
	t.Cleanup(func() {
		close(held.release)

		select {
		case <-held.closed:
		case <-time.After(waitBound):
			assert.Fail(t, "the state writer was not joined")
		}
	})

	rig.awaitGeneration(1)

	select {
	case <-held.entered:
	case <-time.After(waitBound):
		require.FailNow(t, "no state write started", rig.stderr.String())
	}

	// the disk does not answer: the next generations are still applied, and served
	rig.store.publish(5)
	rig.poll()

	status, value := rig.evaluate(tenantKey)
	assert.Equal(t, http.StatusOK, status)
	assert.InDelta(t, 5, value, 0, "the generation published while the write is held is served")
}
