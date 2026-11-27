// Package manifestreader polls the control-plane manifest and delivers verified objects.
//
// A [Reader] sends one conditional GET of the manifest per poll interval (If-None-Match with the ETag it
// holds). While no object is failing, a 304 answer costs nothing else: no object request, no parsing, no snapshot.
// While an object is failing, a round costs the manifest GET plus one GET for each failing object (and, only when
// that fails, one more for each other entry that lists the same checksum). A new manifest is
// decoded and validated with the controlplane package, the generation rules are applied, and only the objects
// whose checksum is not already held are fetched, with a bounded number of concurrent requests. Every object
// is verified with its sha256 checksum before it is served. The reader never lists a bucket and never asks
// for the metadata of an object.
//
// The result of a generation is one immutable [Snapshot], sent on the channel of [Reader.Snapshots] with
// latest-wins delivery: the poller never blocks on a slow consumer. The state of the reader is readable at any
// time, without blocking the poller, from [Reader.Status].
//
// The reader talks to a backend through the small [ObjectStore] interface; [S3Store] is the implementation on
// the S3 client of a named connection of the connection factory, and [FileStore] the one on a local
// directory (a volume mount, a ConfigMap). The storetest package holds the conformance suite every store passes.
// The package is built beside the relay and no running code uses it yet.
package manifestreader
