# Manifest and document model

The control plane publishes three kinds of document to a store. The relay reads them and builds what it serves.
[The control plane package](../go-module/controlplane.md) writes and validates them.

## The pieces

| Piece | What it is |
|---|---|
| Manifest | One small document for one channel. It lists the source objects with a checksum for each. The relay polls it. |
| Source object | A stored file that the manifest lists: a flag object (the flags), a flag-set document or a segments document. |
| Flag set | A named set of flags and API keys. A flag-set document defines it and names the objects that hold its flags. |
| Object name | The logical name of a source object in the manifest. Flag sets refer to objects by name, never by bucket or key. |
| Connection | A named store endpoint of the bootstrap file. A manifest entry names the connection that holds its object. |

A flag set never holds a bucket, a key or a credential. The manifest entry holds the connection, the object key and the
checksum. The bootstrap file holds the bucket and the credentials. The manifest and the documents never hold a
connection, a server setting or an admin key.

## Generation

- A manifest has a `generation`: a whole number that starts at 1 and only grows for a channel.
- The relay keeps the manifest with the highest generation it has accepted. A manifest with a lower generation is
  ignored (outcome `lower-generation`).
- A manifest with the same generation and another set of objects or another checksum is a conflict. It is rejected
  (outcome `generation-conflict`). The same generation with the same content changes nothing.
- A manifest of another channel than the expected one is rejected (outcome `channel-mismatch`).
- The `channel` of the bootstrap file (`controlPlane.manifest.channel`, default `stable`) must equal the one in the
  manifest.

## Checksums and object keys

- The checksum of an object is the sha256 of the exact stored bytes, written in lower-case hex.
- The relay downloads an object, computes the sha256 and compares it with the listed checksum and size. It never
  trusts headers of the store for integrity. A different checksum rejects that object (reason `checksum-mismatch`), a
  different size too (reason `size-mismatch`).
- A key that follows `objects/sha256/<2 hex>/<hex>.<ext>` is content addressed: the bytes decide the key, so a key never
  gets other content. The format recommends it. The relay accepts any absolute key: not empty, no leading slash (reason `invalid-key` otherwise).
- An object whose checksum the relay already holds is not downloaded, parsed or rebuilt again. An object that
  several flag sets list is downloaded once.

## Order of writing

The publisher writes the objects first and the manifest last, with a conditional write. A manifest then never lists
content that is not yet readable. `semaphore-cli publish` does this. See [publishing](../tooling/publish.md).

## Last known good

Each level keeps its own last valid version. A failure at one level never freezes the levels above it.

| Level | Failure | What the relay keeps |
|---|---|---|
| Manifest | not reachable, not valid, lower generation | the held manifest and everything built from it |
| Source object | missing, wrong checksum, not parsable | the last valid content of that object; the other objects apply |
| Flag-set entry | an invalid entry in a valid document | the last valid definition of that flag set |
| Flag | an invalid definition in a valid object | the last valid definition of that flag; the other flags apply |

An object or flag that a valid manifest removes is not a failure: it disappears. With no earlier version the item is
absent, everything else loads, and its state is `pending`. A [state directory](../relay-proxy/bootstrap-config.md#controlplane)
lets a restarted replica serve the last good content while the store is unreachable.

The state of each level is visible in `/info` under `config` and in the metrics, see the [runbook](../relay-proxy/runbook.md).
