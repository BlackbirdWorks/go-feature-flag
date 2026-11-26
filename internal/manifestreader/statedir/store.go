package statedir

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"

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
	// versionContent is the content of the VERSION file: a layout of another version is treated as empty.
	versionContent = "semaphore-manifest-state 1\n"
)

// Closed reasons of a failed save, in [manifestreader.StateStatus.LastSaveError] and the metrics.
const (
	// ReasonNoSpace is a full disk or an exhausted quota.
	ReasonNoSpace = "no-space"
	// ReasonDenied is a permission failure or a read-only file system.
	ReasonDenied = "denied"
	// ReasonIO is any other failure of the file system (EIO, a failed fsync or rename).
	ReasonIO = "io"
	// ReasonTooLarge is a manifest whose current objects do not fit in [Config.MaxBytes]: the ones that fit
	// were saved.
	ReasonTooLarge = "too-large"
)

// Config configures a [Store].
type Config struct {
	// FS is the file system; nil is the real one ([NewOSFS]).
	FS FS
	// Logger receives the warnings of the store; nil discards. It logs closed codes, never paths or contents.
	Logger *slog.Logger
	// Dir is the state directory. Required. It is created (mode 0700) when it does not exist. A symbolic link,
	// a file, or a directory of another user is refused with [ErrUnsafe].
	Dir string
	// MaxBytes bounds the bytes of objects on disk: the current objects first, then the stale ones that still
	// fit. The manifest file comes on top. Zero is [manifestreader.DefaultMaxTotalBytes], which is the cap on the
	// current objects of the reader, so the current objects always fit with the default reader limits.
	MaxBytes int64
}

// Store is a state directory open for use. Build it with [New], give [Store.Hooks] to the reader, run
// [Store.Run] on a goroutine, and call [Store.Close] after Run returned.
type Store struct {
	root    Root
	log     *slog.Logger
	changed chan struct{}
	wake    chan struct{}
	pending *job
	disk    diskState
	st      manifestreader.StateStatus
	max     int64
	seq     uint64
	done    uint64
	tmpSeq  uint64
	mu      sync.Mutex
	ioMu    sync.Mutex
	writing bool
	layout  bool
	closed  bool
}

// diskState is what the store knows to be on disk and durable.
type diskState struct {
	objects map[string]struct{}
	digest  string
}

// New opens and locks the state directory. It does not read it: that is [Store.Hooks] Restore.
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

	root, err := fsys.Open(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("statedir: %w", err)
	}

	for _, name := range []string{versionFile, lockFile, manifestFile, objectsDir} {
		info, statErr := root.Lstat(name)
		if statErr == nil && info.Mode()&fs.ModeSymlink != 0 {
			_ = root.Close()

			return nil, fmt.Errorf("%w: %s is a symbolic link", ErrUnsafe, name)
		}
	}

	err = root.Lock()
	if err != nil {
		_ = root.Close()

		return nil, fmt.Errorf("statedir: lock: %w", err)
	}

	maxBytes := cfg.MaxBytes
	if maxBytes <= 0 {
		maxBytes = manifestreader.DefaultMaxTotalBytes
	}

	return &Store{
		root: root, log: logger, max: maxBytes, wake: make(chan struct{}, 1), changed: make(chan struct{}),
		disk: diskState{objects: map[string]struct{}{}}, st: manifestreader.StateStatus{Enabled: true},
	}, nil
}

// Hooks returns the hooks of the reader: Restore, Save and StateStatus.
func (s *Store) Hooks() manifestreader.Hooks {
	return manifestreader.Hooks{Restore: s.restore, Save: s.save, StateStatus: s.status}
}

// Close releases the directory and its lock. Call it after [Store.Run] returned. A second call does nothing.
func (s *Store) Close() error {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()

	if s.closed {
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

// Flush returns when everything saved before the call is written (or superseded by something written). It
// needs [Store.Run] running; ctx bounds the wait.
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

		res := s.write(job)

		s.mu.Lock()
		s.writing = false
		s.done = job.seq

		if res.written {
			s.st.Saves++
			s.st.LastSave = clock.Now()
			s.st.LastSaveError = res.reason

			if res.reason != "" {
				s.st.SaveErrors++
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
	case errors.Is(err, errNoSpace):
		return ReasonNoSpace
	case errors.Is(err, fs.ErrPermission), errors.Is(err, errReadOnly):
		return ReasonDenied
	}

	return ReasonIO
}
