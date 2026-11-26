// Package statedir keeps the last accepted manifest and its verified objects in a directory, so a restart serves
// the last content even when the store is unreachable, and costs one conditional GET (a 304) and no object GET
// when it is not.
//
// It plugs into the manifest reader through [Store.Hooks]: the reader stays ignorant of the file system. The
// design in short:
//
//   - Layout: VERSION, LOCK, manifest.json (an envelope: the ETag, the manifest, the checksums of its objects and
//     a digest of all of it) and objects/<sha256> (content addressed, mode 0600, directory 0700).
//   - Every file is written to a temporary file in its own directory, fsynced, renamed and the directory is
//     fsynced. Objects go first and the manifest last, so the manifest on disk never names an object that is not
//     there; garbage collection runs after the manifest rename.
//   - Everything read back is verified: the manifest by its digest and every object by its sha256. A file that
//     fails is ignored on its own: a bad object costs one GET, a bad manifest a cold start. Nothing unverified is
//     ever returned to the reader.
//   - The writes happen on one background writer fed latest-wins ([Store.Run]): [Store.Hooks] Save only queues,
//     so a slow disk never delays a poll. A failed write is reported in the state status, never stops polling and
//     is retried with the next snapshot.
//   - A directory has one owner at a time: an exclusive advisory lock (flock, on systems that have it) makes a
//     second process fail with [ErrLocked]. Without the lock the layout is still last-writer-wins and cannot be
//     corrupted: every file is complete or absent, and a reader verifies what it reads.
//
// All file system access goes through the small [FS] and [Root] interfaces, so tests inject failures.
package statedir
