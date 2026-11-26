package statedir

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"

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
	// staleNames are the names that were served stale, with the content they are served from.
	staleNames []staleRef
	seq        uint64
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

	for name, state := range persist.Snapshot.Objects {
		if state.State != manifestreader.StateStale || state.Bytes == nil {
			continue
		}

		job.staleNames = append(job.staleNames, staleRef{
			Name: name, Kind: state.Kind, Format: state.Format, Algorithm: state.Checksum.Algorithm,
			Sum: state.Checksum.Value,
		})

		if _, dup := seen[state.Checksum.Value]; !dup {
			seen[state.Checksum.Value] = struct{}{}

			job.stale = append(job.stale, object{sum: state.Checksum.Value, data: state.Bytes})
		}
	}

	slices.SortFunc(job.current, func(a, b object) int { return cmp.Compare(a.sum, b.sum) })
	slices.SortFunc(job.stale, func(a, b object) int { return cmp.Compare(a.sum, b.sum) })
	slices.SortFunc(job.staleNames, func(a, b staleRef) int { return cmp.Compare(a.Name, b.Name) })

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
	partial bool
}

// write saves a job. The order is the safety argument, and it never changes: the lock is held (taken when the
// directory was opened, and again by every reopen); the VERSION file is valid; the destinations are checked; the
// budget is applied; the objects are written; the manifest is written; what the manifest does not reference is
// removed. Nothing is probed, removed or written in a directory whose VERSION is foreign or unreadable. Nothing is
// written when the state on disk is already this one.
func (s *Store) write(ctx context.Context, job *job) outcome {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()

	if s.root == nil || s.needReopen {
		// an earlier write failed (or an earlier reopen did): the directory is opened again and validated before
		// anything is trusted
		err := s.reopen(ctx)
		if err != nil {
			s.needReopen = true

			return outcome{written: true, reason: reasonOf(err)}
		}
	}

	// 2. ownership first: the VERSION file is read and validated before anything is probed, removed or written
	_, err := inspect(s.root)
	if err != nil {
		s.forget()

		// the directory at the path may be another one by the next write: it is opened again by path
		s.needReopen = true

		return outcome{written: true, reason: reasonOf(err)}
	}

	// 3. destinations: what is on disk is listed and typed, and what cannot be written is left out before the
	// budget is applied, so it never takes the place of an object that could be kept
	s.reconcile()

	usable, blocked := s.withoutBlocked(job)
	keep, staleKept, partial := s.fit(usable)
	partial = partial || blocked

	refs := make([]objectRef, len(keep))

	for i := range keep {
		refs[i] = objectRef{Sum: keep[i].sum, Size: int64(len(keep[i].data))}
	}

	digest := digestOf(job.etag, job.manifest, refs, staleKept)

	if s.upToDate(digest, refs) {
		return outcome{partial: partial}
	}

	err = s.persist(job, keep, refs, staleKept, digest)

	if errors.Is(err, fs.ErrNotExist) {
		// something under us vanished (the objects directory, the whole directory): rebuild from nothing
		s.forget()

		err = s.reopen(ctx)
		if err == nil {
			err = s.persist(job, keep, refs, staleKept, digest)
		}
	}

	if err != nil {
		s.forget()

		s.needReopen = true

		return outcome{written: true, reason: reasonOf(err)}
	}

	return outcome{written: true, partial: partial}
}

// forget drops what the store believes about the disk: the next write checks and rebuilds the layout and writes
// every object again.
func (s *Store) forget() {
	s.layout = false
	s.disk = diskState{objects: map[string]struct{}{}}
}

// reconcile checks what the store believes about the disk against the directory: an object that is not there any
// more, a manifest copy that is gone, a layout that vanished are forgotten, so the next write puts them back. It
// costs a listing of objects/ per write.
func (s *Store) reconcile() {
	s.present = map[string]struct{}{}

	objects, err := s.root.ReadDir(objectsDir)
	if err != nil {
		s.forget()

		return
	}

	// "present": a regular file under the name of a checksum. What is trusted is the name and the type, not the
	// content: a file that this package wrote was renamed into place whole, and one that is wrong in any other way
	// is caught at restore by its checksum, and written again by the next write anyway (presence only drives the
	// budget priority, never the skipping of a write).
	for _, name := range objects {
		if isSum(name) && s.isRegular(objectsDir+"/"+name) {
			s.present[name] = struct{}{}
		}
	}

	if !s.layout {
		return
	}

	rootNames, err := s.root.ReadDir(".")
	if err != nil || !slices.Contains(rootNames, versionFile) {
		s.forget()

		return
	}

	// "durable": known to be written by this store since it was last in doubt. Only that lets a write be skipped.
	for sum := range s.disk.objects {
		if _, ok := s.present[sum]; !ok {
			delete(s.disk.objects, sum)
		}
	}

	if !slices.Contains(rootNames, manifestFile) || !s.isRegular(manifestFile) {
		s.disk.digest = ""
	}
}

// isRegular reports whether name is a regular file: a name that became a directory, a symbolic link or anything
// else is not what the store wrote, and is written again (a rename replaces a symbolic link itself and never
// follows it).
func (s *Store) isRegular(name string) bool {
	info, err := s.root.Lstat(name)

	return err == nil && info.Mode().IsRegular()
}

// withoutBlocked returns the job without the objects whose file name is taken by a directory that cannot be
// removed (it is not empty): they are not saved, and the save is partial when one of them is current. An empty
// directory is removed. Nothing is ever removed recursively.
func (s *Store) withoutBlocked(job *job) (*job, bool) {
	var (
		current []object
		stale   []object
		blocked = map[string]struct{}{}
		partial bool
	)

	filter := func(objects []object, isCurrent bool) []object {
		usable := make([]object, 0, len(objects))

		for _, obj := range objects {
			if _, known := s.disk.objects[obj.sum]; !known && s.destinationBlocked(objectsDir+"/"+obj.sum) {
				blocked[obj.sum] = struct{}{}
				partial = partial || isCurrent

				continue
			}

			usable = append(usable, obj)
		}

		return usable
	}

	current, stale = filter(job.current, true), filter(job.stale, false)

	if len(blocked) == 0 {
		return job, false
	}

	usable := *job
	usable.current, usable.stale = current, stale
	usable.staleNames = nil

	for _, ref := range job.staleNames {
		if _, gone := blocked[ref.Sum]; !gone {
			usable.staleNames = append(usable.staleNames, ref)
		}
	}

	return &usable, partial
}

// destinationBlocked reports whether name is a directory that cannot be replaced; an empty one is removed.
func (s *Store) destinationBlocked(name string) bool {
	info, err := s.root.Lstat(name)
	if err != nil || !info.IsDir() {
		return false
	}

	return s.root.Remove(name) != nil
}

// reopen opens the directory again (it may have been removed and made anew) and takes the lock.
func (s *Store) reopen(ctx context.Context) error {
	if s.root != nil {
		_ = s.root.Close()
		s.root = nil
	}

	root, err := openOwned(ctx, s.fsys, s.dir, s.log)
	if err != nil {
		s.noteParentSync(ctx, nil, err)

		return err
	}

	s.root = root
	s.needReopen = false

	s.noteParentSync(ctx, root, nil)

	return nil
}

// fit returns the objects to keep within the byte budget, the stale names whose content is kept, and whether
// the save is partial. The current objects come first. When they fit, the stale ones that still fit are added.
// When the current objects alone are above the budget, the stale ones are dropped and the current ones are kept
// in this order: the ones that are on disk already (they cost nothing to keep, and a save never removes an object
// that the snapshot still needs if the budget allows it), then the others, each smallest first (ties by checksum),
// which favours not losing what a restart can already restore over the count of contents.
func (s *Store) fit(job *job) ([]object, []staleRef, bool) {
	var total int64
	for _, obj := range job.current {
		total += int64(len(obj.data))
	}

	var (
		keep    []object
		partial bool
	)

	if total <= s.max {
		keep = slices.Clone(job.current)
		used := total

		for _, obj := range s.byPriority(job.stale) {
			if used+int64(len(obj.data)) <= s.max {
				used += int64(len(obj.data))

				keep = append(keep, obj)
			}
		}
	} else {
		partial = true

		var used int64

		for _, obj := range s.byPriority(job.current) {
			if used+int64(len(obj.data)) > s.max {
				continue
			}

			used += int64(len(obj.data))

			keep = append(keep, obj)
		}
	}

	slices.SortFunc(keep, func(a, b object) int { return cmp.Compare(a.sum, b.sum) })

	kept := make(map[string]struct{}, len(keep))
	for _, obj := range keep {
		kept[obj.sum] = struct{}{}
	}

	var stale []staleRef

	for _, ref := range job.staleNames {
		if _, ok := kept[ref.Sum]; ok {
			stale = append(stale, ref)
		}
	}

	return keep, stale, partial
}

// byPriority orders objects for the budget: the ones on disk first, then smallest first, ties by checksum.
func (s *Store) byPriority(objects []object) []object {
	ordered := slices.Clone(objects)

	slices.SortFunc(ordered, func(left, right object) int {
		_, onA := s.present[left.sum]
		_, onB := s.present[right.sum]

		switch {
		case onA && !onB:
			return -1
		case onB && !onA:
			return 1
		}

		return cmp.Or(cmp.Compare(len(left.data), len(right.data)), cmp.Compare(left.sum, right.sum))
	})

	return ordered
}

func (s *Store) upToDate(digest string, refs []objectRef) bool {
	if !s.layout || s.disk.digest != digest {
		return false
	}

	for _, ref := range refs {
		if _, ok := s.disk.objects[ref.Sum]; !ok {
			return false
		}
	}

	return true
}

// persist is the save sequence. Every step that can fail returns at once; what was written until then is either
// an object file that is complete and named by its checksum, or a temporary file nobody reads. The order is the
// crash-safety argument: object files, fsync of objects/, the manifest, fsync of the directory, and only then the
// garbage collection.
func (s *Store) persist(job *job, keep []object, refs []objectRef, stale []staleRef, digest string) error {
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

	if s.destinationBlocked(manifestFile) {
		return fmt.Errorf("%w: %s is a directory that is not empty", ErrUnsafe, manifestFile)
	}

	content, err := json.Marshal(&envelope{
		Version: layoutVersion, ETag: job.etag, Digest: digest, Objects: refs, Stale: stale, Manifest: job.manifest,
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
	file, tmp, err := s.createTemp(path.Dir(name))
	if err != nil {
		return err
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

// tmpAttempts bounds the tries for a free temporary name.
const tmpAttempts = 8

// createTemp creates a temporary file in dir under a random name, exclusively: two writers on one directory (the
// lock is what normally prevents that, and some systems have none) never share a temporary name, and a name that is
// taken is replaced by another, up to tmpAttempts times.
func (s *Store) createTemp(dir string) (File, string, error) { //nolint:ireturn // the FS seam returns an interface
	var lastErr error

	for range tmpAttempts {
		name, err := newTmpName()
		if err != nil {
			return nil, "", err
		}

		tmp := path.Join(dir, name)

		file, err := s.root.Create(tmp)
		if err == nil {
			return file, tmp, nil
		}

		if !errors.Is(err, fs.ErrExist) {
			return nil, "", fmt.Errorf("statedir: %w", err)
		}

		lastErr = err
	}

	return nil, "", fmt.Errorf("statedir: no free temporary name: %w", lastErr)
}

// ensureLayout makes the directory ready: VERSION is ours and objects/ exists. A directory that is not ours is
// never touched: without VERSION it must hold nothing but our LOCK and temporary files ([ErrUnsafe]), and a
// VERSION of another layout is [ErrForeignVersion]. VERSION is written first and made durable, so that a crash
// leaves a directory that is recognisably ours.
func (s *Store) ensureLayout() error {
	if s.layout {
		return nil
	}

	haveVersion, err := inspect(s.root)
	if err != nil {
		return err
	}

	if !haveVersion {
		err = s.writeAtomic(versionFile, []byte(versionContent))
		if err == nil {
			err = s.root.SyncDir(".")
		}

		if err != nil {
			return fmt.Errorf("statedir: %w", err)
		}
	}

	err = s.root.MkdirAll(objectsDir)
	if err == nil {
		err = s.root.SyncDir(".")
	}

	if err != nil {
		return fmt.Errorf("statedir: %w", err)
	}

	s.layout = true

	return nil
}

// collect removes the object files that the manifest on disk does not name, and the leftovers of a crashed
// write. Only names this package writes are removed (a checksum in hex, ".tmp-" and a number), and only under
// a VERSION that is ours. It runs after the manifest rename: a failure here costs disk space until the next
// save, never consistency, so it is not reported as a failed save.
func (s *Store) collect(keep []object) {
	wanted := make(map[string]struct{}, len(keep))
	for _, obj := range keep {
		wanted[obj.sum] = struct{}{}
	}

	removed := s.collectObjects(wanted)

	// leftovers of a crashed writer are removed by pattern, and only where the lock gives this store the directory:
	// without a lock another writer may be in the middle of a write, so its temporary files are not ours to remove
	if isExclusive(s.root) {
		removed = s.collectRootTemps() || removed
	}

	if removed {
		_ = s.root.SyncDir(objectsDir)
	}
}

// collectObjects removes the files of objects/ that the manifest does not name: checksums, and (under the lock)
// temporary files. It reports whether it removed one.
func (s *Store) collectObjects(wanted map[string]struct{}) bool {
	names, err := s.root.ReadDir(objectsDir)
	if err != nil {
		return false
	}

	exclusive := isExclusive(s.root)
	removed := false

	for _, name := range names {
		_, isWanted := wanted[name]
		removable := isSum(name) || (isOurTmp(name) && exclusive)

		if isWanted || !removable {
			continue
		}

		if s.root.Remove(objectsDir+"/"+name) == nil {
			removed = true

			delete(s.disk.objects, name)
		}
	}

	return removed
}

// collectRootTemps removes the temporary files that a crashed writer left in the directory itself.
func (s *Store) collectRootTemps() bool {
	names, err := s.root.ReadDir(".")
	if err != nil {
		return false
	}

	removed := false

	for _, name := range names {
		if isOurTmp(name) && s.root.Remove(name) == nil {
			removed = true
		}
	}

	return removed
}
