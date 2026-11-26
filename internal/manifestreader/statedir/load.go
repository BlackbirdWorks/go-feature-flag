package statedir

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// restore is the Restore hook: it loads the saved manifest and the objects that verify, and nothing else.
func (s *Store) restore() (*manifestreader.Restored, error) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()

	restored, discarded, err := s.load()

	result := manifestreader.RestoreRestored

	switch {
	case err != nil:
		result = manifestreader.RestoreFailed
	case restored == nil:
		result = manifestreader.RestoreEmpty
	case discarded > 0:
		result = manifestreader.RestorePartial
	}

	s.mu.Lock()
	s.st.Restore = result
	s.st.DiscardedObjects = discarded

	if restored != nil {
		s.st.RestoredObjects = len(restored.Objects)
	}

	s.mu.Unlock()

	if err != nil {
		return nil, err
	}

	if restored == nil {
		return nil, manifestreader.ErrNothingRestored
	}

	return restored, nil
}

// load reads the directory. It returns nil without an error when there is no usable state (nothing saved, another
// layout version, a manifest file that fails its digest), and an error when the directory cannot be read.
func (s *Store) load() (*manifestreader.Restored, int, error) {
	ctx := context.Background()

	version, err := s.root.ReadFile(versionFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}

	if err != nil {
		return nil, 0, fmt.Errorf("statedir: read layout version: %w", err)
	}

	if string(version) != versionContent {
		s.log.LogAttrs(ctx, slog.LevelWarn, "state directory: another layout version, starting empty")

		return nil, 0, nil
	}

	raw, err := s.root.ReadFile(manifestFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}

	if err != nil {
		return nil, 0, fmt.Errorf("statedir: read manifest: %w", err)
	}

	var env envelope

	if !validEnvelope(raw, &env) {
		s.log.LogAttrs(ctx, slog.LevelWarn, "state directory: manifest copy is corrupt, starting empty")

		return nil, 0, nil
	}

	restored := &manifestreader.Restored{ETag: env.ETag, Manifest: env.Manifest}
	objects := make(map[string]struct{}, len(env.Objects))
	discarded := 0

	for _, sum := range env.Objects {
		content, ok := s.readObject(sum)
		if !ok {
			discarded++

			continue
		}

		restored.Objects = append(restored.Objects, content)
		objects[sum] = struct{}{}
	}

	if discarded > 0 {
		s.log.LogAttrs(ctx, slog.LevelWarn, "state directory: objects discarded, they are fetched again",
			slog.Int("count", discarded))
	}

	s.layout = true
	s.disk = diskState{objects: objects, digest: env.Digest}

	return restored, discarded, nil
}

// validEnvelope decodes the manifest copy and checks its version, the order of its objects and its digest.
func validEnvelope(raw []byte, env *envelope) bool {
	err := json.Unmarshal(raw, env)

	return err == nil && env.Version == layoutVersion && slices.IsSorted(env.Objects) &&
		digestOf(env.ETag, env.Manifest, env.Objects) == env.Digest
}

// readObject returns the content of the object file of sum when it exists and its sha256 is sum.
func (s *Store) readObject(sum string) ([]byte, bool) {
	if !isSum(sum) {
		return nil, false
	}

	content, err := s.root.ReadFile(objectsDir + "/" + sum)
	if err != nil {
		return nil, false
	}

	if controlplane.ChecksumOf(content).Value != sum {
		return nil, false
	}

	return content, true
}
