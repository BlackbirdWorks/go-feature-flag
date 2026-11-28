package segmentadmin

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	// ErrEmptyInput is returned by an import that read no valid id, unless AllowEmpty is set: swapping it in would
	// wipe the segment.
	ErrEmptyInput = errors.New(
		"the input holds no valid id: an empty import would wipe the segment (use --allow-empty)",
	)
	// ErrKeysLayout is returned by an import of a segment with the keys layout: there is no single key to swap, and
	// the other protocols leave a half built segment visible to the relay.
	ErrKeysLayout = errors.New("import needs the set layout: the keys layout has no atomic swap (use add and remove)")
	// ErrTempGone is returned when the temporary key is gone before the swap (it expired or was removed): nothing
	// was swapped.
	ErrTempGone = errors.New("the temporary key is gone before the swap: raise --temp-ttl")
	// ErrIncomplete is returned when the temporary key does not hold what the run wrote (it expired and was
	// recreated, or changed): nothing was swapped.
	ErrIncomplete = errors.New("the temporary key does not hold the members that were written: nothing was swapped")
	// ErrLockLost is returned when another run holds the lock when this one writes or swaps (the lock expired):
	// nothing was swapped.
	ErrLockLost = errors.New("the import lock is held by another run: nothing was swapped")
	// ErrImportRunning is returned when another import of the segment holds the lock. A lock that a dead run left
	// expires after its ttl.
	ErrImportRunning = errors.New("another import of this segment is running (a dead run's lock expires by its ttl)")
	// ErrPartial is returned when an add or a remove applied only a part of the ids.
	ErrPartial = errors.New("partially applied")
	// ErrNoSlotTag is returned for a key that cannot get a temporary key in its cluster slot.
	ErrNoSlotTag = errors.New("the key has a hash tag that cannot be shared by a temporary key")
	// ErrBadOptions is returned for options out of their bounds.
	ErrBadOptions = errors.New("invalid options")
	// ErrSwapped is returned when the swap worked and a later step did not: the segment is the new one.
	ErrSwapped = errors.New("the change is in the store, but a later step failed")
	// ErrStore is the root of the errors of a store command. The command is named, never its arguments.
	ErrStore = errors.New("segment store")
)

const (
	// scanCount is the COUNT hint of a SCAN call.
	scanCount = 1000
	// ttlMissing and ttlNone are the replies of PTTL for a missing key and a key without expiry.
	ttlMissing = -2
	ttlNone    = -1
)

// pttlOf reads the reply of PTTL in milliseconds: whether the key exists, and its ttl (zero for none).
func pttlOf(millis int64) (bool, time.Duration, error) {
	switch millis {
	case ttlMissing:
		return false, 0, nil
	case ttlNone:
		return true, 0, nil
	}

	return true, max(time.Duration(millis)*time.Millisecond, time.Millisecond), nil
}

// Mutator is the commands that change a segment. Every method takes the keys it touches as they are; a method that
// takes several keys of a cluster sends one command for each.
type Mutator interface {
	// SAdd adds members to a set and returns how many were new. With ttl > 0 it sets the expiry of the key in the same
	// pipeline, so that every batch refreshes it.
	SAdd(ctx context.Context, key string, members []string, ttl time.Duration) (int64, error)
	// SRem removes members from a set and returns how many were there.
	SRem(ctx context.Context, key string, members []string) (int64, error)
	// SetKeys creates the keys (one key for each member of the keys layout) and returns how many were new.
	SetKeys(ctx context.Context, keys []string) (int64, error)
	// Unlink removes keys without blocking the server (UNLINK) and returns how many existed. A server that has no
	// UNLINK is sent DEL, and fellBack says so.
	Unlink(ctx context.Context, keys ...string) (removed int64, fellBack bool, err error)
	// SetValue sets a string key.
	SetValue(ctx context.Context, key, value string) error
}

// Inspector is the commands that read a segment.
type Inspector interface {
	// Value reads a string key; ok is false when it does not exist.
	Value(ctx context.Context, key string) (value string, ok bool, err error)
	// SCard is the cardinality of a set, zero for a missing key.
	SCard(ctx context.Context, key string) (int64, error)
	// KeyTTL tells whether the key exists and, when it expires, in how long (zero: never).
	KeyTTL(ctx context.Context, key string) (exists bool, ttl time.Duration, err error)
	// CountKeys counts the keys that start with prefix, with SCAN, and stops at limit: more is true when it did.
	CountKeys(ctx context.Context, prefix string, limit int) (count int, more bool, err error)
}

// CommitResult is what the conditional swap did.
type CommitResult int

const (
	// Committed means that the new set is live.
	Committed CommitResult = iota
	// LockLost means that the lock did not hold the token of the run: nothing changed.
	LockLost
	// TempGone means that the temporary key does not exist: nothing changed.
	TempGone
	// CountMismatch means that the temporary key does not hold the members that were written: nothing changed.
	CountMismatch
)

// Commit is the conditional swap of an import. Every key must be in one cluster slot.
type Commit struct {
	// Lock, Tmp, Live and Old are the keys. Last is the key that names the temporary key.
	Lock, Tmp, Live, Old, Last string
	// Token is the token of the run.
	Token string
	// Want is the cardinality that the temporary key must have. Zero is the explicit empty import: the live key is
	// removed, if the lock still holds the token.
	Want int64
	// OldTTL is the expiry of the replaced set.
	OldTTL time.Duration
}

// Locker is the commands of the lock and of the conditional swap of an import.
type Locker interface {
	// Lock takes the lock key with the token for ttl (SET NX PX) and tells whether it was free.
	Lock(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	// Unlock removes the lock and the last key if, and only if, the lock still holds the token.
	Unlock(ctx context.Context, lock, last, token string) error
	// StageAdd adds members to the temporary key, and refreshes its expiry and the one of the lock, if, and only if,
	// the lock holds the token (held is false otherwise, and nothing was written).
	StageAdd(ctx context.Context, lock, tmp, token string, members []string, ttl time.Duration) (
		added int64, held bool, err error)
	// Commit swaps in one step on the server: if the lock holds the token, the temporary key exists and has Want
	// members, the live key moves to Old (with its expiry), the temporary key becomes the live key and loses its
	// expiry. Otherwise nothing changes. fellBack is true when the empty path had to use DEL.
	Commit(ctx context.Context, commit *Commit) (result CommitResult, fellBack bool, err error)
}

// Store is the write side of a segment store. An implementation exists for each client library.
type Store interface {
	Mutator
	Inspector
	Locker
	// Ping checks the connection.
	Ping(ctx context.Context) error
	// Close releases the client.
	Close()
}

// IsUnknownCommand tells whether a server error says that the command does not exist.
func IsUnknownCommand(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unknown command")
}

// IsNoSuchKey tells whether a server error is the one of RENAME of a missing key.
func IsNoSuchKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such key")
}

// globEscape escapes the characters of a SCAN MATCH pattern.
func globEscape(prefix string) string {
	var out strings.Builder

	for _, char := range prefix {
		if strings.ContainsRune(`*?[]\`, char) {
			out.WriteByte('\\')
		}

		out.WriteRune(char)
	}

	return out.String()
}

// ScanPattern is the SCAN MATCH pattern of the keys that start with prefix.
func ScanPattern(prefix string) string { return globEscape(prefix) + "*" }
