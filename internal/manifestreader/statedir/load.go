package statedir

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

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

// envelopeLimit is the largest manifest copy: the base64 of the largest manifest, and the entries of the largest
// number of objects.
func (s *Store) envelopeLimit() int64 {
	return s.maxMan/3*4 + 4 + int64(s.maxObjs)*envelopeEntry*2 + envelopeSlack
}

// load reads the directory. It returns nil without an error when there is no usable state (nothing saved, a
// manifest copy that is not a regular file, is too large or fails its digest), and an error when the directory
// cannot be read.
func (s *Store) load() (*manifestreader.Restored, int, error) {
	ctx := context.Background()

	raw, err := s.root.ReadFile(manifestFile, s.envelopeLimit())

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, 0, nil
	case errors.Is(err, ErrNotRegular), errors.Is(err, ErrFileTooLarge):
		s.log.LogAttrs(ctx, slog.LevelWarn, "state directory: manifest copy is not usable, starting empty")

		return nil, 0, nil
	case err != nil:
		return nil, 0, fmt.Errorf("statedir: read manifest: %w", err)
	}

	var env envelope

	if !s.validEnvelope(raw, &env) {
		s.log.LogAttrs(ctx, slog.LevelWarn, "state directory: manifest copy is corrupt, starting empty")

		return nil, 0, nil
	}

	restored := &manifestreader.Restored{ETag: env.ETag, Manifest: env.Manifest}
	objects := make(map[string]struct{}, len(env.Objects))
	discarded := 0

	for _, ref := range env.Objects {
		content, ok := s.readObject(ref)
		if !ok {
			discarded++

			continue
		}

		restored.Objects = append(restored.Objects, content)
		objects[ref.Sum] = struct{}{}
	}

	for _, ref := range env.Stale {
		if _, ok := objects[ref.Sum]; ok {
			restored.Stale = append(restored.Stale, manifestreader.StaleObject{
				Name: ref.Name, Kind: ref.Kind, Format: ref.Format,
				Checksum: controlplane.Checksum{Algorithm: ref.Algorithm, Value: ref.Sum},
			})
		}
	}

	if discarded > 0 {
		s.log.LogAttrs(ctx, slog.LevelWarn, "state directory: objects discarded, they are fetched again",
			slog.Int("count", discarded))
	}

	s.layout = true
	s.disk = diskState{objects: objects, digest: env.Digest}

	return restored, discarded, nil
}

// validEnvelope decodes the manifest copy and checks its version, the bounds of its lists, the form of its
// entries, their order and its digest.
func (s *Store) validEnvelope(raw []byte, env *envelope) bool {
	err := json.Unmarshal(raw, env)
	if err != nil || env.Version != layoutVersion || len(env.Objects) > s.maxObjs || len(env.Stale) > s.maxObjs ||
		int64(len(env.Manifest)) > s.maxMan {
		return false
	}

	return s.validObjects(env.Objects) && validStale(env.Stale) &&
		digestOf(env.ETag, env.Manifest, env.Objects, env.Stale) == env.Digest
}

// validObjects checks that the objects are checksums in order, with a size within the object bound.
func (s *Store) validObjects(refs []objectRef) bool {
	for i, ref := range refs {
		if !isSum(ref.Sum) || ref.Size < 1 || ref.Size > s.maxObj || (i > 0 && refs[i-1].Sum >= ref.Sum) {
			return false
		}
	}

	return true
}

// validStale checks that the stale names are in order and name a checksum.
func validStale(refs []staleRef) bool {
	for i, ref := range refs {
		if !isSum(ref.Sum) || (i > 0 && refs[i-1].Name > ref.Name) {
			return false
		}
	}

	return true
}

// readObject returns the content of the object file of ref when it is a regular file of the recorded size and
// its sha256 is the checksum.
func (s *Store) readObject(ref objectRef) ([]byte, bool) {
	content, err := s.root.ReadFile(objectsDir+"/"+ref.Sum, ref.Size)
	if err != nil || int64(len(content)) != ref.Size {
		return nil, false
	}

	if controlplane.ChecksumOf(content).Value != ref.Sum {
		return nil, false
	}

	return content, true
}
