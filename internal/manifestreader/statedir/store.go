package statedir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

// Names of the files of the layout (version 1).
const (
	versionFile   = "VERSION"
	lockFile      = "LOCK"
	manifestFile  = "manifest.json"
	objectsDir    = "objects"
	tmpPrefix     = ".tmp-"
	layoutVersion = 1
	// versionContent is the content of the VERSION file. A directory with another VERSION is not ours.
	versionContent = "semaphore-manifest-state 1\n"
	// versionLimit bounds the read of the VERSION file.
	versionLimit = 64
	// envelopeSlack and envelopeEntry bound the manifest copy beyond the base64 of the manifest: the JSON frame, and
	// per object (or per stale name) its checksum, size and names.
	envelopeSlack = 64 << 10
	envelopeEntry = 1 << 10
)

// Closed reasons of a failed save, in [manifestreader.StateStatus.LastSaveError] and the metrics.
const (
	// ReasonNoSpace is a full disk or an exhausted quota.
	ReasonNoSpace = "no-space"
	// ReasonDenied is a permission failure or a read-only file system.
	ReasonDenied = "denied"
	// ReasonIO is any other failure of the file system (EIO, a failed fsync or rename).
	ReasonIO = "io"
	// ReasonUnsafe is a directory that is not ours any more (a foreign layout version or foreign files).
	ReasonUnsafe = "unsafe"
)

// Closed reasons of a state directory that is not used, in [manifestreader.StateStatus.DisabledReason].
const (
	// DisabledUnsafe is [ErrUnsafe]: a symbolic link, a file, a world-writable or unusable directory, or a
	// directory that holds foreign files and no VERSION.
	DisabledUnsafe = "unsafe"
	// DisabledLocked is [ErrLocked]: another process uses the directory.
	DisabledLocked = "locked"
	// DisabledForeignVersion is [ErrForeignVersion].
	DisabledForeignVersion = "foreign-version"
	// DisabledParentSync is [ErrParentSync]: a flush of a parent of the directory that was created failed.
	DisabledParentSync = "parent-sync"
	// DisabledError is any other failure to open the directory.
	DisabledError = "error"
)

// Config configures a [Store].
type Config struct {
	// FS is the file system; nil is the real one ([NewOSFS]).
	FS FS
	// Logger receives the messages of the store; nil discards. It logs closed codes, never paths or contents.
	Logger *slog.Logger
	// Dir is the state directory. Required. A directory that does not exist is created with mode 0700. See
	// [OSFS] for the rules of an existing one.
	Dir string
	// MaxBytes bounds the bytes of objects on disk (the manifest copy comes on top). Zero is
	// [manifestreader.DefaultMaxTotalBytes], the cap on the current objects of the reader. When the current
	// objects do not fit, the stale ones are dropped first and then the current ones are kept in this order:
	// the contents already on disk are kept first and the rest of the budget goes to the new ones, smallest first;
	// such a save is a partial save, not an error.
	MaxBytes int64
	// MaxManifestBytes, MaxObjectBytes and MaxObjects bound what is read back (use the limits of the reader).
	// Zero selects [manifestreader.DefaultMaxManifestBytes], [manifestreader.DefaultMaxObjectBytes] and
	// [controlplane.DefaultMaxObjects]. A file above its bound is discarded without being read.
	MaxManifestBytes int64
	MaxObjectBytes   int64
	MaxObjects       int
}

// Store is a state directory open for use. Build it with [New] (or [OpenOrDisable]), give [Store.Hooks] to the
// reader, run [Store.Run] on a goroutine, and call [Store.Close] after Run returned.
type Store struct {
	fsys    FS
	root    Root
	log     *slog.Logger
	changed chan struct{}
	wake    chan struct{}
	pending *job
	disk    diskState
	// present is what a listing of objects/ found as regular files named by a checksum when the last write began: it
	// drives the budget priority. disk is the narrower knowledge that lets a write be skipped.
	present map[string]struct{}
	dir     string
	st      manifestreader.StateStatus
	max     int64
	maxMan  int64
	maxObj  int64
	maxObjs int
	seq     uint64
	done    uint64
	tmpSeq  uint64
	mu      sync.Mutex
	ioMu    sync.Mutex
	writing bool
	layout  bool
	closed  bool
	// needReopen is set by a failed write: the next write opens the directory again and validates it.
	needReopen bool
	// parentLogged tells that the skipped flush of a parent was logged.
	parentLogged bool
}

// diskState is what the store knows to be on disk and durable. It is dropped after any failed write.
type diskState struct {
	objects map[string]struct{}
	digest  string
}

// New opens and locks the state directory and checks that it is ours. It does not read the state: that is
// [Store.Hooks] Restore. The errors tell the caller what to do (see [OpenOrDisable]):
//
//   - [ErrUnsafe]: a symbolic link, a file, a world-writable directory, a directory the process cannot read, write
//     or lock, or a directory that has no VERSION and holds anything but our LOCK and temporary files. Nothing
//     was written.
//   - [ErrLocked]: another process or store holds the directory.
//   - [ErrForeignVersion]: VERSION names another layout version. Nothing was read, written or deleted.
//   - [ErrNoDir]: no directory configured.
func New(cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, ErrNoDir
	}

	fsys := cfg.FS
	if fsys == nil {
		fsys = NewOSFS()
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	root, err := openOwned(fsys, cfg.Dir)
	if err != nil {
		return nil, err
	}

	store := &Store{
		fsys: fsys, root: root, dir: cfg.Dir, log: logger, wake: make(chan struct{}, 1), changed: make(chan struct{}),
		max:     orDefault(cfg.MaxBytes, manifestreader.DefaultMaxTotalBytes),
		maxMan:  orDefault(cfg.MaxManifestBytes, manifestreader.DefaultMaxManifestBytes),
		maxObj:  orDefault(cfg.MaxObjectBytes, manifestreader.DefaultMaxObjectBytes),
		maxObjs: int(orDefault(int64(cfg.MaxObjects), controlplane.DefaultMaxObjects)),
		disk:    diskState{objects: map[string]struct{}{}}, st: manifestreader.StateStatus{Enabled: true},
	}

	store.noteParentSync(context.Background(), root, nil)

	return store, nil
}

// parentSyncReporter is implemented by a root that tells how the flush of the parents of a created directory went.
type parentSyncReporter interface{ ParentSync() string }

func orDefault(value, fallback int64) int64 {
	if value <= 0 {
		return fallback
	}

	return value
}

// openLocked opens and locks the directory, and refuses a layout name that is a symbolic link. It reports whether
// the lock file did not exist before.
func openLocked(fsys FS, dir string) (Root, bool, error) { //nolint:ireturn // the FS seam returns an interface
	root, err := fsys.Open(dir)
	if err != nil {
		return nil, false, wrapPermission(fmt.Errorf("statedir: %w", err))
	}

	_, lockErr := root.Lstat(lockFile)
	created := errors.Is(lockErr, fs.ErrNotExist)

	for _, name := range []string{versionFile, lockFile, manifestFile, objectsDir} {
		info, statErr := root.Lstat(name)
		if statErr == nil && info.Mode()&fs.ModeSymlink != 0 {
			_ = root.Close()

			return nil, false, fmt.Errorf("%w: %s is a symbolic link", ErrUnsafe, name)
		}
	}

	err = root.Lock()
	if err != nil {
		_ = root.Close()

		return nil, false, fmt.Errorf("statedir: lock: %w", err)
	}

	return root, created, nil
}

// openOwned is the one way to open a state directory, for [New] and for a reopen by the writer: it opens the path,
// takes the lock, and validates that the directory is ours. On refusal it undoes what this call created (the lock
// file, when it did not exist) and closes the directory, so a directory that is not ours is left exactly as it was
// found.
func openOwned(fsys FS, dir string) (Root, error) { //nolint:ireturn // the FS seam returns an interface
	root, created, err := openLocked(fsys, dir)
	if errors.Is(err, ErrParentSync) {
		// the opener remembers the flush that failed: one more try costs nothing (the memory is the opener's, so
		// it lives as long as this opener; a fresh process cannot know, and treats an existing path as flushed)
		root, created, err = openLocked(fsys, dir)
	}

	if err != nil {
		return nil, err
	}

	_, err = inspect(root)
	if err != nil {
		if created {
			_ = root.Remove(lockFile)
		}

		_ = root.Close()

		return nil, err
	}

	return root, nil
}

// inspect tells whether the directory is ours. It reports whether VERSION is there (and ours); without VERSION the
// directory must hold nothing but our LOCK and temporary files, else it is not ours.
func inspect(root Root) (bool, error) {
	version, err := root.ReadFile(versionFile, versionLimit)

	switch {
	case err == nil && string(version) == versionContent:
		return true, nil
	case err == nil:
		return false, ErrForeignVersion
	case errors.Is(err, ErrNotRegular), errors.Is(err, ErrFileTooLarge):
		return false, fmt.Errorf("%w: VERSION is not a version file", ErrUnsafe)
	case !errors.Is(err, fs.ErrNotExist):
		return false, wrapPermission(fmt.Errorf("statedir: read layout version: %w", err))
	}

	names, err := root.ReadDir(".")
	if err != nil {
		return false, wrapPermission(fmt.Errorf("statedir: list directory: %w", err))
	}

	for _, name := range names {
		if name != lockFile && !isOurTmp(name) {
			return false, fmt.Errorf("%w: it holds files that are not ours and no VERSION", ErrUnsafe)
		}
	}

	return false, nil
}

// OpenOrDisable is [New] with the policy the relay uses: when the directory is refused ([ErrUnsafe], [ErrLocked],
// [ErrForeignVersion], or any other failure) the relay runs without a state directory and never fails to start.
// It logs once at error level, and returns hooks that only report the refusal in the status
// ([manifestreader.StateStatus.Restore] is [manifestreader.RestoreDisabled] and DisabledReason is one of the
// Disabled* codes), so the restore metric shows it. The store is nil then, and the reason is not empty.
func OpenOrDisable(cfg Config) (*Store, manifestreader.Hooks, string) {
	store, err := New(cfg)
	if err == nil {
		hooks := store.Hooks()

		return store, hooks, ""
	}

	reason := DisabledError

	switch {
	case errors.Is(err, ErrUnsafe):
		reason = DisabledUnsafe
	case errors.Is(err, ErrLocked):
		reason = DisabledLocked
	case errors.Is(err, ErrForeignVersion):
		reason = DisabledForeignVersion
	case errors.Is(err, ErrParentSync):
		reason = DisabledParentSync
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	logger.LogAttrs(context.Background(), slog.LevelError,
		"state directory refused: the relay runs without one", slog.String("reason", reason))

	status := manifestreader.StateStatus{Restore: manifestreader.RestoreDisabled, DisabledReason: reason}

	return nil, manifestreader.Hooks{StateStatus: func() manifestreader.StateStatus { return status }}, reason
}

// Hooks returns the hooks of the reader: Restore, Save and StateStatus.
func (s *Store) Hooks() manifestreader.Hooks {
	return manifestreader.Hooks{Restore: s.restore, Save: s.save, StateStatus: s.status}
}

// Close releases the directory and its lock. Call it after [Store.Run] returned. A second call does nothing.
func (s *Store) Close() error {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()

	if s.closed || s.root == nil {
		return nil
	}

	s.closed = true

	err := s.root.Close()
	if err != nil {
		return fmt.Errorf("statedir: close: %w", err)
	}

	return nil
}

// Run is the writer: it writes the snapshots queued by Save, one at a time, newest first (an older one that was
// not written yet is replaced). It returns when ctx is done, after it wrote what is queued.
func (s *Store) Run(ctx context.Context) {
	logCtx := context.WithoutCancel(ctx)

	for {
		s.drain(logCtx)

		select {
		case <-ctx.Done():
			s.drain(logCtx)

			return
		case <-s.wake:
		}
	}
}

// Flush returns when the writer is done with everything saved before the call: written, failed (the failure is in
// [manifestreader.StateStatus]: Flush does not report it) or replaced by a newer snapshot. It needs [Store.Run]
// running; ctx bounds the wait.
func (s *Store) Flush(ctx context.Context) error {
	s.mu.Lock()
	target := s.seq

	for s.done < target {
		changed := s.changed
		s.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return fmt.Errorf("statedir: flush: %w", ctx.Err())
		}

		s.mu.Lock()
	}

	s.mu.Unlock()

	return nil
}

// noteParentSync records in the status that the flush of a parent was skipped (once, with a warning that names the
// directory) or is pending after a failed open.
func (s *Store) noteParentSync(ctx context.Context, root Root, openErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch {
	case errors.Is(openErr, ErrParentSync):
		s.st.ParentSync = manifestreader.ParentSyncPending
	case s.st.ParentSync == manifestreader.ParentSyncPending && openErr == nil:
		s.st.ParentSync = ""
	}

	reporter, ok := root.(parentSyncReporter)
	if !ok || reporter.ParentSync() != manifestreader.ParentSyncSkipped {
		return
	}

	s.st.ParentSync = manifestreader.ParentSyncSkipped

	if !s.parentLogged {
		s.parentLogged = true

		s.log.LogAttrs(
			ctx,
			slog.LevelWarn,
			"state directory: its parent could not be flushed (permission): after a power loss the directory may be missing",
			slog.String("path", s.dir),
		)
	}
}

func (s *Store) status() manifestreader.StateStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	status := s.st
	status.Pending = s.pending != nil || s.writing

	return status
}

func (s *Store) drain(ctx context.Context) {
	for {
		s.mu.Lock()
		job := s.pending
		s.pending = nil
		s.writing = job != nil
		s.mu.Unlock()

		if job == nil {
			return
		}

		res := s.write(ctx, job)

		s.mu.Lock()
		s.writing = false
		s.done = job.seq

		if res.written {
			s.st.Saves++
			s.st.LastSave = clock.Now()
			s.st.LastSaveError = res.reason
			s.st.LastSavePartial = res.partial

			if res.reason != "" {
				s.st.SaveErrors++
			}

			if res.partial {
				s.st.PartialSaves++
			}
		}

		close(s.changed)
		s.changed = make(chan struct{})
		s.mu.Unlock()

		if res.reason != "" {
			s.log.LogAttrs(ctx, slog.LevelWarn, "state directory: save failed, polling goes on",
				slog.String("reason", res.reason))
		}
	}
}

// reasonOf maps an error of the file system to a closed code.
func reasonOf(err error) string {
	switch {
	case isNoSpace(err):
		return ReasonNoSpace
	case errors.Is(err, ErrUnsafe), errors.Is(err, ErrForeignVersion):
		return ReasonUnsafe
	case errors.Is(err, fs.ErrPermission), isReadOnly(err):
		return ReasonDenied
	}

	return ReasonIO
}
