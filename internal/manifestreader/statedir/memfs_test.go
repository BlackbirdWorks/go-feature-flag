package statedir_test

import (
	"errors"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

const lockName = "LOCK"

var errCrash = errors.New("simulated crash")

// inode is a file: the data the running process sees and the data that survives a power loss (what was
// synced).
type inode struct {
	data    []byte
	durable []byte
}

// memDisk is the persistent part of the in-memory file system: it survives the stores opened on it. It models
// durability: file data is durable after File.Sync, a directory entry (create, rename, remove) after SyncDir of its
// directory. powerLoss returns the disk as it is after a power cut: only what is durable.
type memDisk struct {
	live      map[string]*inode
	durable   map[string]*inode
	dirs      map[string]bool
	irregular map[string]bool
	locked    bool
	mu        sync.Mutex
}

func newMemDisk() *memDisk {
	return &memDisk{
		live: map[string]*inode{}, durable: map[string]*inode{}, dirs: map[string]bool{".": true},
		irregular: map[string]bool{},
	}
}

func (d *memDisk) names() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]string, 0, len(d.live))
	for name := range d.live {
		out = append(out, name)
	}

	slices.Sort(out)

	return out
}

func (d *memDisk) read(name string) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ino, ok := d.live[name]
	if !ok {
		return nil, false
	}

	return append([]byte(nil), ino.data...), true
}

// write puts a file on the disk as if it had been there for ever (durable).
func (d *memDisk) write(name string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	ino := &inode{data: append([]byte(nil), data...), durable: append([]byte(nil), data...)}
	d.live[name], d.durable[name] = ino, ino
	d.dirs[path.Dir(name)] = true
}

func (d *memDisk) remove(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.live, name)
	delete(d.durable, name)
}

// removeDir removes a directory and every file in it.
func (d *memDisk) removeObjectsDir() {
	const name = "objects"

	d.mu.Lock()
	defer d.mu.Unlock()

	for file := range d.live {
		if strings.HasPrefix(file, name+"/") {
			delete(d.live, file)
			delete(d.durable, file)
		}
	}

	delete(d.dirs, name)
}

// powerLoss is the disk after a power cut: the durable entries with their synced data (an entry whose data was
// never synced is an empty file), nothing else.
func (d *memDisk) powerLoss() *memDisk {
	d.mu.Lock()
	defer d.mu.Unlock()

	lost := newMemDisk()
	maps.Copy(lost.dirs, d.dirs)

	for name, ino := range d.durable {
		data := append([]byte(nil), ino.durable...)
		kept := &inode{data: data, durable: data}
		lost.live[name], lost.durable[name] = kept, kept
	}

	return lost
}

// faults decides which mutating step of a store fails. Steps are counted from 1 over: create, write, sync,
// close, rename, remove, syncdir and mkdir. With crash set, every step from the failing one on fails, as if the
// process had died there.
type faults struct {
	err      error
	gate     func(op string)
	failRead map[string]error
	ops      []string
	steps    int
	failAt   int
	crash    bool
	mu       sync.Mutex
}

// step returns the error of this step and whether it is the failing one.
func (f *faults) step(kind, name string) (bool, error) {
	f.mu.Lock()
	gate := f.gate
	f.steps++
	f.ops = append(f.ops, kind+" "+name)
	failing := f.failAt != 0 && f.steps == f.failAt
	after := f.crash && f.failAt != 0 && f.steps > f.failAt
	err := f.err
	f.mu.Unlock()

	if gate != nil {
		gate(kind)
	}

	if err == nil {
		err = errCrash
	}

	if failing {
		return true, err
	}

	if after {
		return false, errCrash
	}

	return false, nil
}

func (f *faults) readErr(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.failRead[name]
}

func (f *faults) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	count := 0

	for _, entry := range f.ops {
		if strings.HasPrefix(entry, prefix) {
			count++
		}
	}

	return count
}

func (f *faults) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.ops, f.steps = nil, 0
}

// memFS is a statedir.FS on a memDisk. noLock makes Lock a no-op, as on a system without flock.
type memFS struct {
	disk   *memDisk
	faults *faults
	noLock bool
}

func (m *memFS) Open(string) (statedir.Root, error) { //nolint:ireturn // implements the FS seam
	return &memRoot{fsys: m}, nil
}

type memRoot struct {
	fsys   *memFS
	locked bool
}

func (r *memRoot) Lock() error {
	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	if r.fsys.noLock {
		return nil
	}

	if dsk.locked {
		return statedir.ErrLocked
	}

	dsk.locked, r.locked = true, true

	return nil
}

func (r *memRoot) Close() error {
	if r.locked {
		dsk := r.disk()
		dsk.mu.Lock()
		dsk.locked = false
		dsk.mu.Unlock()

		r.locked = false
	}

	return nil
}

func (r *memRoot) ReadFile(name string, limit int64) ([]byte, error) {
	err := r.fsys.faults.readErr(name)
	if err != nil {
		return nil, err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	ino, ok := dsk.live[name]

	switch {
	case !ok:
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	case dsk.irregular[name]:
		return nil, statedir.ErrNotRegular
	case int64(len(ino.data)) > limit:
		return nil, statedir.ErrFileTooLarge
	}

	return append([]byte(nil), ino.data...), nil
}

type memInfo struct {
	name string
	mode fs.FileMode
}

func (i memInfo) Name() string      { return i.name }
func (memInfo) Size() int64         { return 0 }
func (i memInfo) Mode() fs.FileMode { return i.mode }
func (memInfo) ModTime() time.Time  { return time.Time{} }
func (i memInfo) IsDir() bool       { return i.mode.IsDir() }
func (memInfo) Sys() any            { return nil }

func (r *memRoot) Lstat(name string) (fs.FileInfo, error) {
	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	if _, ok := dsk.live[name]; ok {
		return memInfo{name: name, mode: 0o600}, nil
	}

	if dsk.dirs[name] {
		return memInfo{name: name, mode: fs.ModeDir | 0o700}, nil
	}

	return nil, &fs.PathError{Op: "lstat", Path: name, Err: fs.ErrNotExist}
}

func (r *memRoot) Create(name string) (statedir.File, error) { //nolint:ireturn // implements the FS seam
	_, err := r.fsys.faults.step(opCreate, name)
	if err != nil {
		return nil, err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	if _, ok := dsk.live[name]; ok {
		return nil, &fs.PathError{Op: "create", Path: name, Err: fs.ErrExist}
	}

	if !dsk.dirs[path.Dir(name)] {
		return nil, &fs.PathError{Op: "create", Path: name, Err: fs.ErrNotExist}
	}

	ino := &inode{}
	dsk.live[name] = ino

	return &memFile{root: r, ino: ino, name: name}, nil
}

type memFile struct {
	root *memRoot
	ino  *inode
	name string
}

func (f *memFile) Write(data []byte) (int, error) {
	failing, err := f.root.fsys.faults.step("write", f.name)
	if err != nil {
		if failing {
			// a torn write: the first half reached the file
			half := len(data) / 2
			f.append(data[:half])

			return half, err
		}

		return 0, err
	}

	f.append(data)

	return len(data), nil
}

func (f *memFile) Sync() error {
	_, err := f.root.fsys.faults.step("sync", f.name)
	if err != nil {
		return err
	}

	dsk := f.root.disk()
	dsk.mu.Lock()

	f.ino.durable = append([]byte(nil), f.ino.data...)
	dsk.mu.Unlock()

	return nil
}

func (f *memFile) Close() error {
	_, err := f.root.fsys.faults.step("close", f.name)

	return err
}

func (f *memFile) append(data []byte) {
	dsk := f.root.disk()
	dsk.mu.Lock()

	f.ino.data = append(f.ino.data, data...)
	dsk.mu.Unlock()
}

func (r *memRoot) Rename(oldname, newname string) error {
	_, err := r.fsys.faults.step("rename", newname)
	if err != nil {
		return err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	ino, ok := dsk.live[oldname]
	if !ok {
		return &fs.PathError{Op: "rename", Path: oldname, Err: fs.ErrNotExist}
	}

	delete(dsk.live, oldname)
	dsk.live[newname] = ino

	return nil
}

func (r *memRoot) Remove(name string) error {
	if name == lockName {
		return nil // the lock of the memory file system is not a file
	}

	_, err := r.fsys.faults.step("remove", name)
	if err != nil {
		return err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	if _, ok := dsk.live[name]; !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}

	delete(dsk.live, name)

	return nil
}

func (r *memRoot) ReadDir(name string) ([]string, error) {
	err := r.fsys.faults.readErr(name + "/")
	if err != nil {
		return nil, err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	if !dsk.dirs[name] {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}

	var out []string

	for file := range dsk.live {
		if path.Dir(file) == name {
			out = append(out, path.Base(file))
		}
	}

	for dir := range dsk.dirs {
		if dir != "." && path.Dir(dir) == name {
			out = append(out, path.Base(dir))
		}
	}

	return out, nil
}

func (r *memRoot) MkdirAll(name string) error {
	_, err := r.fsys.faults.step("mkdir", name)
	if err != nil {
		return err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	dsk.dirs[name] = true
	dsk.mu.Unlock()

	return nil
}

// SyncDir makes the entries of a directory durable: what is there now stays, what was removed stays removed.
func (r *memRoot) SyncDir(name string) error {
	_, err := r.fsys.faults.step("syncdir", name)
	if err != nil {
		return err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	for file := range dsk.durable {
		if path.Dir(file) == name {
			if _, ok := dsk.live[file]; !ok {
				delete(dsk.durable, file)
			}
		}
	}

	for file, ino := range dsk.live {
		if path.Dir(file) == name {
			dsk.durable[file] = ino
		}
	}

	return nil
}

func (r *memRoot) disk() *memDisk { return r.fsys.disk }
