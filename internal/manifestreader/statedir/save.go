package statedir

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// object is a verified object to keep.
type object struct {
	sum  string
	data []byte
}

// job is one snapshot to write. It owns a copy of the manifest and shares the object bytes, which are immutable.
type job struct {
	etag     string
	manifest []byte
	current  []object
	stale    []object
	seq      uint64
}

// save is the Save hook: it queues the snapshot and returns at once. A snapshot that was not written yet is
// replaced (latest wins). The work on the poller is the copy of the manifest and one pass over the objects.
func (s *Store) save(persist manifestreader.Persist) {
	if persist.Snapshot == nil {
		return
	}

	job := &job{etag: persist.ETag, manifest: bytes.Clone(persist.Manifest)}
	seen := map[string]struct{}{}

	for _, state := range persist.Snapshot.Objects {
		if state.State == manifestreader.StateOK && state.Bytes != nil {
			if _, dup := seen[state.Checksum.Value]; !dup {
				seen[state.Checksum.Value] = struct{}{}

				job.current = append(job.current, object{sum: state.Checksum.Value, data: state.Bytes})
			}
		}
	}

	for _, state := range persist.Snapshot.Objects {
		if state.State != manifestreader.StateOK && state.Bytes != nil {
			if _, dup := seen[state.Checksum.Value]; !dup {
				seen[state.Checksum.Value] = struct{}{}

				job.stale = append(job.stale, object{sum: state.Checksum.Value, data: state.Bytes})
			}
		}
	}

	byName := func(a, b object) int { return cmp.Compare(a.sum, b.sum) }
	slices.SortFunc(job.current, byName)
	slices.SortFunc(job.stale, byName)

	s.mu.Lock()
	s.seq++
	job.seq = s.seq
	s.pending = job
	s.mu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// outcome is the result of writing one job.
type outcome struct {
	reason  string
	written bool
}

// write saves a job: the objects that are not on disk, then the manifest, then the garbage collection. Nothing
// is written when the state on disk is already this one.
func (s *Store) write(job *job) outcome {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()

	keep, over := s.fit(job)
	sums := make([]string, len(keep))

	for i := range keep {
		sums[i] = keep[i].sum
	}

	digest := digestOf(job.etag, job.manifest, sums)

	if s.upToDate(digest, sums) {
		return outcome{reason: reasonIf(over)}
	}

	err := s.persist(job, keep, sums, digest)
	if err != nil {
		return outcome{written: true, reason: reasonOf(err)}
	}

	return outcome{written: true, reason: reasonIf(over)}
}

func reasonIf(tooLarge bool) string {
	if tooLarge {
		return ReasonTooLarge
	}

	return ""
}

// fit returns the objects to keep within the byte budget: the current ones first, then the stale ones. The second
// result tells that a current object did not fit.
func (s *Store) fit(job *job) ([]object, bool) {
	var (
		used int64
		over bool
	)

	keep := make([]object, 0, len(job.current)+len(job.stale))

	for _, obj := range job.current {
		if used+int64(len(obj.data)) > s.max {
			over = true

			continue
		}

		used += int64(len(obj.data))

		keep = append(keep, obj)
	}

	for _, obj := range job.stale {
		if used+int64(len(obj.data)) > s.max {
			continue
		}

		used += int64(len(obj.data))

		keep = append(keep, obj)
	}

	slices.SortFunc(keep, func(a, b object) int { return cmp.Compare(a.sum, b.sum) })

	return keep, over
}

func (s *Store) upToDate(digest string, sums []string) bool {
	if s.disk.digest != digest {
		return false
	}

	for _, sum := range sums {
		if _, ok := s.disk.objects[sum]; !ok {
			return false
		}
	}

	return true
}

// persist is the save sequence. Every step that can fail returns at once; what was written until then is either
// an object file that is complete and named by its checksum, or a temporary file nobody reads.
func (s *Store) persist(job *job, keep []object, sums []string, digest string) error {
	err := s.ensureLayout()
	if err != nil {
		return err
	}

	var written []string

	for _, obj := range keep {
		if _, ok := s.disk.objects[obj.sum]; ok {
			continue
		}

		err = s.writeAtomic(objectsDir+"/"+obj.sum, obj.data)
		if err != nil {
			return err
		}

		written = append(written, obj.sum)
	}

	if len(written) > 0 {
		// the object files are durable before the manifest names them
		err = s.root.SyncDir(objectsDir)
		if err != nil {
			return fmt.Errorf("statedir: %w", err)
		}
	}

	for _, sum := range written {
		s.disk.objects[sum] = struct{}{}
	}

	content, err := json.Marshal(&envelope{
		Version: layoutVersion, ETag: job.etag, Digest: digest, Objects: sums, Manifest: job.manifest,
	})
	if err != nil {
		return fmt.Errorf("statedir: encode manifest: %w", err)
	}

	err = s.writeAtomic(manifestFile, content)
	if err != nil {
		return err
	}

	err = s.root.SyncDir(".")
	if err != nil {
		return fmt.Errorf("statedir: %w", err)
	}

	// the new manifest is durable: what it does not name can go
	s.disk.digest = digest
	s.collect(keep)

	return nil
}

// writeAtomic writes content to a temporary file next to name, flushes it, and renames it over name. A failure
// removes the temporary file when it can.
func (s *Store) writeAtomic(name string, content []byte) error {
	s.tmpSeq++
	tmp := path.Join(path.Dir(name), fmt.Sprintf("%s%d", tmpPrefix, s.tmpSeq))

	removeErr := s.root.Remove(tmp)
	if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		return fmt.Errorf("statedir: %w", removeErr)
	}

	file, err := s.root.Create(tmp)
	if err != nil {
		return fmt.Errorf("statedir: %w", err)
	}

	_, err = file.Write(content)
	if err == nil {
		err = file.Sync()
	}

	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = s.root.Rename(tmp, name)
	}

	if err != nil {
		_ = s.root.Remove(tmp)

		return fmt.Errorf("statedir: write %s: %w", path.Base(name), err)
	}

	return nil
}

// ensureLayout makes the directory ready: objects/ exists and VERSION is ours. A directory of another layout
// version (or with no version file) is emptied of what this package would have written.
func (s *Store) ensureLayout() error {
	if s.layout {
		return nil
	}

	version, err := s.root.ReadFile(versionFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("statedir: %w", err)
	}

	writeVersion := err != nil || string(version) != versionContent
	if writeVersion {
		err = s.reset()
		if err != nil {
			return err
		}
	}

	err = s.root.MkdirAll(objectsDir)
	if err != nil {
		return fmt.Errorf("statedir: %w", err)
	}

	if writeVersion {
		err = s.writeAtomic(versionFile, []byte(versionContent))
		if err == nil {
			err = s.root.SyncDir(".")
		}

		if err != nil {
			return fmt.Errorf("statedir: %w", err)
		}
	}

	s.layout = true

	return nil
}

// reset removes the files of this package from a directory of another layout.
func (s *Store) reset() error {
	err := s.root.Remove(manifestFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("statedir: %w", err)
	}

	names, err := s.root.ReadDir(objectsDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("statedir: %w", err)
	}

	for _, name := range names {
		if isSum(name) || strings.HasPrefix(name, tmpPrefix) {
			removeErr := s.root.Remove(objectsDir + "/" + name)
			if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
				return fmt.Errorf("statedir: %w", removeErr)
			}
		}
	}

	s.disk = diskState{objects: map[string]struct{}{}}

	return nil
}

// collect removes the object files that the manifest on disk does not name, and the leftovers of a crashed
// write. It runs after the manifest rename: a failure here costs disk space until the next save, never
// consistency, so it is logged and not reported as a failed save.
func (s *Store) collect(keep []object) {
	wanted := make(map[string]struct{}, len(keep))
	for _, obj := range keep {
		wanted[obj.sum] = struct{}{}
	}

	names, err := s.root.ReadDir(objectsDir)
	if err != nil {
		return
	}

	removed := false

	for _, name := range names {
		_, isWanted := wanted[name]
		if isWanted || (!isSum(name) && !strings.HasPrefix(name, tmpPrefix)) {
			continue
		}

		if s.root.Remove(objectsDir+"/"+name) == nil {
			removed = true

			delete(s.disk.objects, name)
		}
	}

	rootNames, err := s.root.ReadDir(".")
	if err == nil {
		for _, name := range rootNames {
			if strings.HasPrefix(name, tmpPrefix) && s.root.Remove(name) == nil {
				removed = true
			}
		}
	}

	if removed {
		_ = s.root.SyncDir(objectsDir)
	}
}
