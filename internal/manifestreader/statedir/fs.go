package statedir

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sync"

	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// Errors of opening a state directory.
var (
	// ErrLocked is returned when another process (or another store) holds the directory.
	ErrLocked = errors.New("statedir: directory is in use by another owner")
	// ErrUnsafe is returned for a directory that the process cannot own: a symlink, a file, a directory of
	// another user, or one whose mode cannot be made private.
	ErrUnsafe = errors.New("statedir: directory is not safe to use")
	// ErrNoDir is returned when [Config.Dir] is empty.
	ErrNoDir = errors.New("statedir: no directory configured")
	// ErrForeignVersion is returned when VERSION names another layout version: the directory belongs to another
	// version of the software. Nothing is read from it, written to it or deleted from it.
	ErrForeignVersion = errors.New("statedir: directory has another layout version")
	// ErrNotRegular is what [Root.ReadFile] returns for a name that is not a regular file (a FIFO, a device, a
	// directory, a symbolic link): such a file is discarded and never opened.
	ErrNotRegular = errors.New("statedir: not a regular file")
	// ErrFileTooLarge is what [Root.ReadFile] returns for a file above its limit: it is not read.
	ErrFileTooLarge = errors.New("statedir: file above its limit")
	// ErrParentSync is returned by [OSFS.Open] when the flush of a parent of a directory it created failed (not for lack
	// of permission): the flush is retried by the next Open.
	ErrParentSync = errors.New("statedir: the parent directory could not be flushed")
)

// FS opens a state directory. [NewOSFS] is the real one; tests wrap it or replace it to inject failures.
type FS interface {
	// Open prepares dir (creating it with mode 0700 when it does not exist) and returns the root of it. It
	// returns [ErrUnsafe] for a directory the process cannot own.
	Open(dir string) (Root, error)
}

// Root is an open state directory. Names are slash separated and relative to the directory; none may leave it.
type Root interface {
	// Lock takes the exclusive lock of the directory without waiting; it returns [ErrLocked] when it is held. The
	// lock is released by calling release, which also closes the lock file so that the file can be removed (a file
	// that is open cannot be removed on every system), by Close, and by the end of the process.
	Lock() (release func() error, err error)
	// ReadFile returns the content of a regular file of at most limit bytes. A missing file is [fs.ErrNotExist],
	// a name that is not a regular file [ErrNotRegular], a larger file [ErrFileTooLarge]; none of them is opened
	// past its type and size check, and the read itself is limited.
	ReadFile(name string, limit int64) ([]byte, error)
	// Lstat returns the information of a name without following a symlink.
	Lstat(name string) (fs.FileInfo, error)
	// Create creates a new file of mode 0600 for writing; it fails when the name exists.
	Create(name string) (File, error)
	// Rename replaces newname by oldname atomically.
	Rename(oldname, newname string) error
	// Remove removes a file.
	Remove(name string) error
	// ReadDir returns the names of a directory, in no particular order.
	ReadDir(name string) ([]string, error)
	// MkdirAll creates a directory of mode 0700 and its parents.
	MkdirAll(name string) error
	// SyncDir flushes the entries of a directory to disk.
	SyncDir(name string) error
	// Close releases the lock and the directory.
	Close() error
}

// exclusiveRoot is implemented by a root that can tell whether the lock is really held.
type exclusiveRoot interface{ Exclusive() bool }

// isExclusive tells whether this store has the directory to itself: its lock is held. A root that cannot say is not
// trusted with it.
func isExclusive(root Root) bool {
	reporter, ok := root.(exclusiveRoot)

	return ok && reporter.Exclusive()
}

// File is a file being written.
type File interface {
	io.Writer
	// Sync flushes the data to disk.
	Sync() error
	Close() error
}

// OSFS is the file system of the process, on [os.Root]: a name cannot escape the directory through a symlink.
// A directory is accepted when it is not a symbolic link, is not world-writable, and the process can read,
// write and lock it. Its owner does not matter: a volume that Kubernetes mounts with fsGroup is owned by root and
// group-writable. A directory that does not exist is created with mode 0700; an existing one keeps its mode.
type OSFS struct {
	// syncDir flushes a directory; nil is the real one. A test replaces it.
	syncDir func(string) error
	// pending remembers the flushes that failed, by state directory, until they succeed.
	pending *pendingFlushes
	// flushDir flushes an open directory; nil is the one of the platform. A test replaces it.
	flushDir func(*os.File) error
	// beforeFlock, afterLock and afterRelease are hooks of tests (nil in production) for the lock of the roots.
	beforeFlock  func()
	afterLock    func()
	afterRelease func()
}

// pendingFlushes are the parent directories that a failed flush left to flush, by the state directory they lead to.
type pendingFlushes struct {
	byDir map[string]pendingFlush
	mu    sync.Mutex
}

// pendingFlush is what a failed open left: the parents still to flush, and whether an earlier parent was skipped
// for lack of permission (that fact must survive until the open that succeeds).
type pendingFlush struct {
	parents []string
	skipped bool
}

func (p *pendingFlushes) get(dir string) pendingFlush {
	if p == nil {
		return pendingFlush{}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	state := p.byDir[dir]
	state.parents = slices.Clone(state.parents)

	return state
}

func (p *pendingFlushes) set(dir string, state pendingFlush) {
	if p == nil {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(state.parents) == 0 {
		delete(p.byDir, dir)

		return
	}

	state.parents = slices.Clone(state.parents)
	p.byDir[dir] = state
}

// NewOSFS returns the real file system.
func NewOSFS() OSFS {
	return OSFS{syncDir: syncPath, pending: &pendingFlushes{byDir: map[string]pendingFlush{}}}
}

// Open implements [FS]. A directory that Open creates is made durable: the parent of each directory it created is
// flushed, top down, so the path survives a power loss with the first save. A flush that fails is remembered and
// retried by the next Open of the same directory (an existing path does not mean its entry is durable), and Open
// returns [ErrParentSync] until it succeeds. A parent that cannot be opened for the flush because of permissions is
// skipped: the state directory is a cache, so it is used, and the root reports
// [manifestreader.ParentSyncSkipped] (after a power loss the directory entry itself may be missing, and the relay
// then starts as on a first run).
func (o OSFS) Open(dir string) (Root, error) { //nolint:ireturn // implements the FS seam, which returns an interface
	clean := filepath.Clean(dir)

	var pending pendingFlush

	info, err := os.Lstat(clean)
	if errors.Is(err, fs.ErrNotExist) {
		pending.parents, err = o.create(clean)
		if err == nil {
			info, err = os.Lstat(clean)
		}
	} else {
		pending = o.pending.get(clean)
	}

	if err != nil {
		return nil, wrapPermission(fmt.Errorf("statedir: open directory: %w", err))
	}

	switch {
	case info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0:
		return nil, fmt.Errorf("%w: it is a symbolic link or a reparse point", ErrUnsafe)
	case !info.IsDir():
		return nil, fmt.Errorf("%w: it is not a directory", ErrUnsafe)
	case worldWritable(info):
		return nil, fmt.Errorf("%w: it is writable by everybody", ErrUnsafe)
	}

	skipped, err := o.flush(clean, pending)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(clean)
	if err != nil {
		return nil, wrapPermission(fmt.Errorf("statedir: open directory: %w", err))
	}

	parentSync := ""
	if skipped {
		parentSync = manifestreader.ParentSyncSkipped
	}

	flush := o.flushDir
	if flush == nil {
		flush = flushDir
	}

	return &osRoot{
		root: root, parentSync: parentSync, flush: flush,
		beforeFlock: o.beforeFlock, afterLock: o.afterLock, afterRelease: o.afterRelease,
	}, nil
}

// create makes the directory and the missing directories above it, and returns the parents to flush, top down.
func (OSFS) create(clean string) ([]string, error) {
	var created []string

	for current := clean; ; {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}

		created = append(created, current)

		parent := filepath.Dir(current)
		if parent == current {
			break
		}

		current = parent
	}

	err := os.MkdirAll(clean, dirMode)
	if err != nil {
		return nil, fmt.Errorf("statedir: create directory: %w", err)
	}

	parents := make([]string, 0, len(created))
	for _, made := range slices.Backward(created) {
		parents = append(parents, filepath.Dir(made))
	}

	return parents, nil
}

// flush flushes the parents, in order. A permission error skips that parent (and is reported); any other error
// stops, remembers what is left (this parent included), and fails with [ErrParentSync]. When all is done nothing
// stays pending.
func (o OSFS) flush(dir string, pending pendingFlush) (bool, error) {
	syncDir := o.syncDir
	if syncDir == nil {
		syncDir = syncPath
	}

	skipped := pending.skipped

	for i, parent := range pending.parents {
		err := syncDir(parent)

		switch {
		case err == nil:
		case errors.Is(err, fs.ErrPermission):
			skipped = true
		default:
			o.pending.set(dir, pendingFlush{parents: pending.parents[i:], skipped: skipped})

			return skipped, fmt.Errorf("%w: sync %s: %w", ErrParentSync, parent, err)
		}
	}

	o.pending.set(dir, pendingFlush{})

	return skipped, nil
}

// wrapPermission makes a refusal of the system a [ErrUnsafe]: a directory the process cannot use is not safe to use.
func wrapPermission(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: %w", ErrUnsafe, err)
	}

	return err
}

const (
	dirMode  = 0o700
	fileMode = 0o600
)

type osRoot struct {
	// beforeFlock, afterLock and afterRelease are hooks of tests, nil in production: they run after the lock file is
	// opened and before it is locked, after the lock is taken, and after it is released.
	beforeFlock  func()
	afterLock    func()
	afterRelease func()
	// flush flushes an open directory: flushDir of the platform, or the one of a test.
	flush      func(*os.File) error
	root       *os.Root
	lock       *os.File
	parentSync string
}

// ParentSync reports [manifestreader.ParentSyncSkipped] when the flush of a created parent was skipped for lack of
// permission.
func (r *osRoot) ParentSync() string { return r.parentSync }

func (r *osRoot) Lock() (func() error, error) {
	if r.lock != nil {
		return r.unlock, nil
	}

	for range lockAttempts {
		file, err := r.root.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, fileMode)
		if err != nil {
			return nil, wrapPermission(fmt.Errorf("statedir: open lock file: %w", err))
		}

		if r.beforeFlock != nil {
			r.beforeFlock()
		}

		err = lockFileExclusive(file)
		if err != nil {
			_ = file.Close()

			return nil, err
		}

		if r.atPath(file) {
			r.lock = file

			if r.afterLock != nil {
				r.afterLock()
			}

			return r.unlock, nil
		}

		// the opener that held this file before us removed it (it was refused): what we locked is not at the path
		// any more, and a later opener would lock another file. Take the one that is there.
		_ = file.Close()
	}

	return nil, fmt.Errorf("%w: the lock file keeps being replaced", ErrLocked)
}

// lockAttempts bounds the tries to lock the file that is at the path.
const lockAttempts = 5

func (r *osRoot) ReadFile(name string, limit int64) ([]byte, error) {
	info, err := r.root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("statedir: stat %s: %w", name, err)
	}

	switch {
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, name)
	case info.Size() > limit:
		return nil, fmt.Errorf("%w: %s", ErrFileTooLarge, name)
	}

	file, err := r.root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("statedir: open %s: %w", name, err)
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, fmt.Errorf("statedir: read %s: %w", name, err)
	}

	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s", ErrFileTooLarge, name)
	}

	return data, nil
}

func (r *osRoot) Lstat(name string) (fs.FileInfo, error) {
	info, err := r.root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("statedir: stat %s: %w", name, err)
	}

	return info, nil
}

func (r *osRoot) Create(name string) (File, error) { //nolint:ireturn // the FS seam is an interface by design
	file, err := r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if err != nil {
		return nil, fmt.Errorf("statedir: create %s: %w", name, err)
	}

	return file, nil
}

func (r *osRoot) Rename(oldname, newname string) error {
	err := r.root.Rename(oldname, newname)
	if err != nil {
		return fmt.Errorf("statedir: rename %s: %w", oldname, err)
	}

	return nil
}

func (r *osRoot) Remove(name string) error {
	err := r.root.Remove(name)
	if err != nil {
		return fmt.Errorf("statedir: remove %s: %w", name, err)
	}

	return nil
}

func (r *osRoot) ReadDir(name string) ([]string, error) {
	dir, err := r.root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("statedir: open %s: %w", name, err)
	}

	defer func() { _ = dir.Close() }()

	names, err := dir.Readdirnames(-1)
	if err != nil {
		return nil, fmt.Errorf("statedir: list %s: %w", name, err)
	}

	return names, nil
}

func (r *osRoot) MkdirAll(name string) error {
	err := r.root.MkdirAll(name, dirMode)
	if err != nil {
		return fmt.Errorf("statedir: create directory %s: %w", name, err)
	}

	return nil
}

func (r *osRoot) SyncDir(name string) error {
	dir, err := r.root.Open(path.Clean(name))
	if err != nil {
		return fmt.Errorf("statedir: open %s: %w", name, err)
	}

	err = r.flush(dir)
	_ = dir.Close()

	if err != nil {
		return fmt.Errorf("statedir: sync %s: %w", name, err)
	}

	return nil
}

// UnlinkWhileLocked tells how a lock file is removed on this system (see unlinkWhileLocked).
func (*osRoot) UnlinkWhileLocked() bool { return unlinkWhileLocked }

// Exclusive tells that the lock is really held: not on a system without a lock, and not after it was released.
func (r *osRoot) Exclusive() bool { return hasFileLock && r.lock != nil }

func (r *osRoot) Close() error {
	var lockErr error
	if r.lock != nil {
		lockErr = r.lock.Close()
	}

	err := r.root.Close()
	if err != nil {
		return fmt.Errorf("statedir: close: %w", err)
	}

	if lockErr != nil {
		return fmt.Errorf("statedir: close lock: %w", lockErr)
	}

	return nil
}

// atPath tells that the locked file is the file at the path of the lock (the same file, not one that was unlinked
// and made again). Where there is no lock it is always true.
func (r *osRoot) atPath(file *os.File) bool {
	if !hasFileLock {
		return true
	}

	held, err := file.Stat()
	if err != nil {
		return false
	}

	named, err := r.root.Lstat(lockFile)

	return err == nil && os.SameFile(held, named)
}

func (r *osRoot) unlock() error {
	if r.lock == nil {
		return nil
	}

	err := r.lock.Close()
	r.lock = nil

	if r.afterRelease != nil {
		r.afterRelease()
	}

	if err != nil {
		return fmt.Errorf("statedir: close lock: %w", err)
	}

	return nil
}

// syncPath flushes the entries of a directory to disk, by the flushDir of the platform.
func syncPath(name string) error {
	dir, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}

	err = flushDir(dir)
	_ = dir.Close()

	return err
}
