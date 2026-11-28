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
	// ErrNoSlotTag is returned for a key that cannot get a temporary key in its cluster slot.
	ErrNoSlotTag = errors.New("the key has a hash tag that cannot be shared by a temporary key")
	// ErrBadOptions is returned for options out of their bounds.
	ErrBadOptions = errors.New("invalid options")
	// ErrSwapped is returned when the swap worked and a later step did not: the segment is the new one.
	ErrSwapped = errors.New("the change is in the store, but a later step failed")
	// ErrStore is the root of the errors of a store command. The command is named, never its arguments.
	ErrStore = errors.New("segment store")

	errShortReply = errors.New("the reply of the transaction is short")
)

const (
	// scanCount is the COUNT hint of a SCAN call.
	scanCount = 1000
	// swapReplies is the commands in the transaction of a swap.
	swapReplies = 4
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
	// Swap makes tmp the live key and keeps what was live under old, with the expiry oldTTL, in one MULTI/EXEC: a
	// reader sees the whole old set or the whole new set. A live key that does not exist is not an error. The keys
	// must be in one cluster slot.
	Swap(ctx context.Context, live, tmp, old string, oldTTL time.Duration) error
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

// Store is the write side of a segment store. An implementation exists for each client library.
type Store interface {
	Mutator
	Inspector
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
