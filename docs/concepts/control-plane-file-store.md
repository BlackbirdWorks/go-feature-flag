# Control-plane file store

> Status: the file store (`FileStore` in `internal/manifestreader`) is in the code base but not active yet. The relay
> will use it when it switches to config v2 (epic #19, issue #57); until then no setting selects it. The text below
> says "will" for what the relay does after that switch.

The [manifest reader](control-plane-polling.md) reads the manifest and its objects through a small store interface.
The file store serves them from a directory of the machine instead of S3.

## When to use it

- Development and tests: a directory with a manifest and its objects, no emulator needed.
- A mounted volume: a Kubernetes ConfigMap or Secret mount, an NFS or CSI volume that another process fills.
- Not for production fleets that already have a bucket: the S3 store is the one that spreads load and is built for it.

## Layout

Keys are relative slash paths under one root directory. The manifest key and every object `key` of the manifest are
read as `<root>/<key>`:

```text
root/
  manifests/stable.yaml
  objects/sha256/ab/ab12....json
```

The publisher writes the objects first and the manifest last, as for S3. Write each file atomically (write a
temporary file in the same directory, then rename it over the final name). The reader survives a non-atomic writer
(see [Partial writes](#partial-writes)), but an atomic one costs nothing.

## Keys and containment

A key is refused (reason `denied`, not retryable) when it is empty, starts with `/`, ends with `/`, has a `.`, `..`
or empty element (`a//b`), or contains a backslash or a NUL byte. `..data/manifest.yaml` is a valid key: only a whole
element `..` is refused.

Every path is resolved with `os.Root`, so it cannot leave the root:

| Case | Result |
|---|---|
| A symbolic link inside the root that points to a place inside the root (a file, a directory, a chain, `../sibling` that stays inside) | Followed |
| A symbolic link that points outside the root, directly or through a directory link | Refused: `denied` |
| An absolute symbolic link, even one that points back inside the root | Refused: `denied` |
| A symbolic link loop | Refused: `denied` |
| A dangling link, or a file used as a directory (`a.txt/child`) | Missing |

A link inside that stays inside is followed because a ConfigMap mount is a tree of such links.

## What is served

Only regular files. A directory, device, socket or FIFO is refused without being read (`denied`, cause
`ErrNotRegular`). The file is opened without blocking, so a FIFO cannot hang a poll, and the type is read from the
open file, not from an earlier lookup. `Limit` is enforced from the size the file has when it is opened (too large:
no read at all) and again while reading: the store never reads more than `limit + 1` bytes, so a file that grows
during the read is still refused.

| Situation | Result |
|---|---|
| File does not exist (also a missing directory on the path, a dangling link) | Missing: the same result as an S3 `404` |
| Permission denied | `denied`, not retryable |
| Path leaves the root, key refused, not a regular file, root directory gone | `denied`, not retryable |
| Larger than the limit | Too large |
| Any other I/O error (`EIO`, too many open files, a stale NFS handle) | `error`, retryable |
| `VersionID` set | Missing: a file has no versions. Manifests that list a `versionId` for a file connection are not served |
| Context ended | `error`, retryable, the cause is the context error |

The root directory is opened on every call, so a root that is replaced (a remounted volume) is followed, and one that
disappears is `denied` (the S3 equivalent is a missing bucket).

## Change detection and its exact guarantees

The ETag of an object is the quoted sha256 of its bytes: `"<64 hex digits>"`. A conditional read (the reader sends the
ETag it holds) is answered like this:

1. Open the file and check its type and size.
2. Hash the whole file in 32 KiB chunks. The bytes are not kept.
3. The same digest: answer "not modified" (no body). A different digest: read the file again into one buffer and
   answer with the bytes and the ETag of **those bytes**.

Guarantees:

- **"Not modified" is never wrong.** It is answered only after a digest of the whole current content equals the ETag
  the reader holds. The size, the modification time and the inode of the file are not used for that decision.
- **A change is seen at the next poll.** A rewrite in place that keeps the size, the modification time and the inode
  still changes the digest. This is tested.
- **The ETag always describes the bytes returned with it.** A file replaced between the two passes gives the new bytes
  with the new ETag (checked under concurrent writes).
- **The ETag is the same on every machine and after a restart**, so the one saved in the state directory is valid for the next
  process.

Why not a stat-based validator (size + modification time + inode): it is cheap but it is wrong in cases that happen.
Kernels set the modification time from a coarse clock (a few milliseconds), so two writes close together can share it;
`rsync -t`, `cp -p` and `tar` restore old times; a rewrite in place keeps the inode. Same size and same time with other
bytes is not detected by a stat. The reader's checksum check would catch a wrong object, but a wrong "not modified" on
the manifest would hide a new generation, so the store hashes. A stat is used only for what cannot be wrong: the type
and the size limit before any read.

Cost of the hash (commodity 4-core / 8-thread Linux machine, files in the page cache, one core):

| File size | Unchanged poll | Changed poll | Floor: `stat` only |
|---|---|---|---|
| 1 KiB | 14 us | 15 us | 0.6 us |
| 100 KiB | 0.2 ms | 0.4 ms | 0.6 us |
| 4 MiB | 8 ms | 19 ms | 1 us |
| 16 MiB (the manifest limit) | 44 ms | 85 ms | 1 us |

An unchanged poll allocates a fixed 32 KiB whatever the size; a changed one allocates the file once. At the default
60 s interval a 100 KiB manifest costs about 0.0004 % of one core, a 16 MiB one about 0.07 %. Objects are not read
on a quiet poll at all. Run the benchmark with `go test -tags bench -run '^$' -bench FileStore_Poll
./internal/manifestreader`.

## Partial writes

A writer that is not atomic can be caught in the middle by a poll:

- A half-written **object** has another size or other bytes than the manifest lists: the reader refuses it
  (`size-mismatch` or `checksum-mismatch`), serves the last good content of that name, and reads the file again at
  the next poll. The other objects are not affected.
- A half-written **manifest** does not decode (or lists a generation it cannot apply): the reader rejects it and keeps
  the previous one. The next poll reads the finished file, which has another ETag.

A manifest that is cut at a place where it still decodes cannot be told from a complete one: write the manifest atomically.

## ConfigMap and Secret mounts

Kubernetes mounts a ConfigMap as a directory tree of links:

```text
mount/
  ..2024_05_01_10_00_00.123/manifest.yaml     real files
  ..data -> ..2024_05_01_10_00_00.123          swapped atomically on every update
  manifest.yaml -> ..data/manifest.yaml
```

An update writes a new timestamped directory, renames a new `..data` link over the old one, and removes the old
directory. The store follows these links (all relative and inside the mount), so `manifest.yaml` resolves to the files of
the current directory. The swap is atomic: a read sees the old directory or the new one. A read that started on the old
directory just before it was removed can find the file missing; that poll is a failed poll and the next one succeeds.
Remember that kubelet updates a mounted ConfigMap with a delay of up to about a minute, that a ConfigMap is limited to
1 MiB, and that a `subPath` mount never updates: mount the whole directory.

## Request budget

The equivalent of the S3 budget, per replica and per poll interval:

| Situation | File operations |
|---|---|
| Nothing changed (the normal case) | 1 open and read of the manifest file. Nothing else: no directory listing, no object file touched |
| A new manifest that changes `K` objects | The manifest read twice (hash, then read) plus `K` object reads (one pass each, they have no ETag) |
| Failing objects | As in [control-plane polling](control-plane-polling.md#request-budget): one read for each failing object that is due |

The counts do not depend on the number of objects, flag sets or API keys. The cost of a read is the cost of hashing the file
(see above), not of a network round trip, and a local read is not billed.

## Limits and Windows

- A read cannot be cancelled inside a single system call: the context is looked at between reads of 32 KiB. A network
  file system that hangs inside a read hangs that poll until the call returns.
- Linux, macOS and the BSDs are the supported platforms. The code builds for Windows, and `os.Root` rules apply there:
  names such as `NUL` and `COM1` are refused, symbolic links need a privilege to create, and a file that is open for
  writing may be locked against reading. Permission denial by mode bits does not exist on Windows.
- A file that is read as root (or on a file system without permission bits) cannot be made `denied` by its mode.
- `os.Root` does not stop a path from crossing a mount point or reaching a device inside the root; the store refuses
  the device itself (not a regular file) but follows a mount point.

## Writing another store

Every `ObjectStore` has to pass the conformance suite in `internal/manifestreader/storetest`. Give `storetest.Run` a
factory that returns a fresh store with a backend handle:

```go
func TestConformance_MyStore(t *testing.T) {
	t.Parallel()

	report := storetest.Run(t, "mystore", func(t *testing.T) storetest.Fixture {
		store, backend := newStoreAndBackend(t) // backend: Put, Overwrite, Delete, Fail, Hang
		return storetest.Fixture{
			Store: store, Backend: backend,
			Unsupported: map[storetest.Capability]string{
				storetest.CapHang: "why this backend cannot hold a read",
			},
		}
	})

	assert.Equal(t, wantUnsupported, report.Unsupported) // an unsupported case is a decision, assert it
}
```

- A case that needs a fault the backend cannot arrange is reported as unsupported, with the reason, and logged. It is
  never skipped silently, and the test of the store asserts the exact list.
- The cases cover: unconditional and conditional reads, a changed object, an unknown ETag, missing, missing version,
  denied, transient and throttled errors (`Retryable`, `Throttled`, `Reason`), the limit at and one over, an empty and a large
  object, key characters and unclean keys, ETag stability and ETag change for generated contents (including same-size
  in-place rewrites), the bytes and ETag of one answer under concurrent writes, an ended context, cancel and deadline during
  a read, no request left after a cancel, and concurrent reads.
- The suite runs today against the in-memory fake, the file store and the S3 store (the last one with the `docker`
  build tag, on the emulator).
- The ETag must be a function of the content, and a conditional read of unchanged content must return
  `ErrNotModified` with an empty `Object`.
