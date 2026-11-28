package relayapp

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
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
		extra: "auth:\n  adminKeys: [" + adminKey + "]\n", manifestKey: "manifests/missing.yaml",
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

	// the status of the reader is made at the end of each of its rounds: one more round shows the failure
	rig.tick(t)

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

// tick lets the reader poll once more and waits for the end of its round.
func (r *relayRig) tick(t *testing.T) {
	t.Helper()

	select {
	case r.sched.release <- struct{}{}:
	case <-time.After(waitBound):
		require.FailNow(t, "the reader is not waiting for a tick")
	}

	select {
	case <-r.sched.entered:
	case <-time.After(waitBound):
		require.FailNow(t, "the reader did not end its next round")
	}
}
