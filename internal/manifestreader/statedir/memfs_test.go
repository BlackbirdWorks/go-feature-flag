package statedir_test

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

var errCrash = errors.New("simulated crash")

// memDisk is the persistent part of the in-memory file system: it survives the stores opened on it.
type memDisk struct {
	files  map[string][]byte
	dirs   map[string]bool
	locked bool
	mu     sync.Mutex
}

func newMemDisk() *memDisk {
	return &memDisk{files: map[string][]byte{}, dirs: map[string]bool{".": true}}
}

func (d *memDisk) names() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]string, 0, len(d.files))
	for name := range d.files {
		out = append(out, name)
	}

	slices.Sort(out)

	return out
}

func (d *memDisk) read(name string) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	data, ok := d.files[name]

	return append([]byte(nil), data...), ok
}

func (d *memDisk) write(name string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.files[name] = append([]byte(nil), data...)
	d.dirs[path.Dir(name)] = true
}

func (d *memDisk) remove(name string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.files, name)
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
	if r.fsys.noLock {
		return nil
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

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

func (r *memRoot) ReadFile(name string) ([]byte, error) {
	err := r.fsys.faults.readErr(name)
	if err != nil {
		return nil, err
	}

	data, ok := r.disk().read(name)
	if !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}

	return data, nil
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

	if _, ok := dsk.files[name]; ok {
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

	if _, ok := dsk.files[name]; ok {
		return nil, &fs.PathError{Op: opCreate, Path: name, Err: fs.ErrExist}
	}

	if !dsk.dirs[path.Dir(name)] {
		return nil, &fs.PathError{Op: opCreate, Path: name, Err: fs.ErrNotExist}
	}

	dsk.files[name] = nil

	return &memFile{root: r, name: name}, nil
}

type memFile struct {
	root *memRoot
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

	return err
}

func (f *memFile) Close() error {
	_, err := f.root.fsys.faults.step("close", f.name)

	return err
}

func (f *memFile) append(data []byte) {
	d := f.root.disk()
	d.mu.Lock()
	d.files[f.name] = append(d.files[f.name], data...)
	d.mu.Unlock()
}

func (r *memRoot) Rename(oldname, newname string) error {
	_, err := r.fsys.faults.step("rename", newname)
	if err != nil {
		return err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	data, ok := dsk.files[oldname]
	if !ok {
		return &fs.PathError{Op: "rename", Path: oldname, Err: fs.ErrNotExist}
	}

	delete(dsk.files, oldname)
	dsk.files[newname] = data

	return nil
}

func (r *memRoot) Remove(name string) error {
	_, err := r.fsys.faults.step("remove", name)
	if err != nil {
		return err
	}

	dsk := r.disk()
	dsk.mu.Lock()
	defer dsk.mu.Unlock()

	if _, ok := dsk.files[name]; !ok {
		return &fs.PathError{Op: "remove", Path: name, Err: fs.ErrNotExist}
	}

	delete(dsk.files, name)

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

	for file := range dsk.files {
		if path.Dir(file) == name {
			out = append(out, path.Base(file))
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

func (r *memRoot) SyncDir(name string) error {
	_, err := r.fsys.faults.step("syncdir", name)

	return err
}

func (r *memRoot) disk() *memDisk { return r.fsys.disk }
