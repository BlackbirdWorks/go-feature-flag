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
)

// FS opens a state directory. [NewOSFS] is the real one; tests wrap it or replace it to inject failures.
type FS interface {
	// Open prepares dir (creating it with mode 0700 when it does not exist) and returns the root of it. It
	// returns [ErrUnsafe] for a directory the process cannot own.
	Open(dir string) (Root, error)
}

// Root is an open state directory. Names are slash separated and relative to the directory; none may leave it.
type Root interface {
	// Lock takes the exclusive lock of the directory without waiting; it returns [ErrLocked] when it is held.
	// The lock is released by Close (and by the end of the process).
	Lock() error
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
}

// NewOSFS returns the real file system.
func NewOSFS() OSFS { return OSFS{syncDir: syncPath} }

// Open implements [FS].
func (o OSFS) Open(dir string) (Root, error) { //nolint:ireturn // implements the FS seam, which returns an interface
	clean := filepath.Clean(dir)

	info, err := os.Lstat(clean)
	if errors.Is(err, fs.ErrNotExist) {
		err = o.create(clean)
		if err == nil {
			info, err = os.Lstat(clean)
		}
	}

	if err != nil {
		return nil, wrapPermission(fmt.Errorf("statedir: open directory: %w", err))
	}

	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return nil, fmt.Errorf("%w: it is a symbolic link", ErrUnsafe)
	case !info.IsDir():
		return nil, fmt.Errorf("%w: it is not a directory", ErrUnsafe)
	case info.Mode().Perm()&0o002 != 0:
		return nil, fmt.Errorf("%w: it is writable by everybody", ErrUnsafe)
	}

	root, err := os.OpenRoot(clean)
	if err != nil {
		return nil, wrapPermission(fmt.Errorf("statedir: open directory: %w", err))
	}

	return &osRoot{root: root}, nil
}

// create makes the directory and the missing directories above it, and makes their entries durable: the parent of
// each directory that was created is flushed, top down, so the path survives a power loss with the first save.
func (o OSFS) create(clean string) error {
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
		return fmt.Errorf("statedir: create directory: %w", err)
	}

	syncDir := o.syncDir
	if syncDir == nil {
		syncDir = syncPath
	}

	for _, made := range slices.Backward(created) {
		err = syncDir(filepath.Dir(made))
		if err != nil {
			return fmt.Errorf("statedir: sync %s: %w", filepath.Dir(made), err)
		}
	}

	return nil
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
	root *os.Root
	lock *os.File
}

func (r *osRoot) Lock() error {
	if r.lock != nil {
		return nil
	}

	file, err := r.root.OpenFile(lockFile, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return wrapPermission(fmt.Errorf("statedir: open lock file: %w", err))
	}

	err = lockFileExclusive(file)
	if err != nil {
		_ = file.Close()

		return err
	}

	r.lock = file

	return nil
}

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

	err = dir.Sync()
	_ = dir.Close()

	if err != nil {
		return fmt.Errorf("statedir: sync %s: %w", name, err)
	}

	return nil
}

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
