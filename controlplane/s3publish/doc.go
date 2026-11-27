// Package s3publish publishes control-plane documents to an S3 bucket so that a reader polling the manifest never
// sees a half-written state. It is the upload protocol of issue #11, built on the controlplane package.
//
// A Publisher takes the documents of one channel (flag files and flag-set documents), builds the manifest, and
// writes in this order:
//
//  1. Read the current manifest and its ETag (one GetObject). Nothing changed: stop, nothing is written.
//  2. For every object, in order: a HeadObject; if the content-addressed key is absent, a PutObject with
//     If-None-Match: * (create only, never an overwrite). An existing object with the same size is success.
//  3. The manifest last, with a conditional PutObject: If-Match with the ETag read in step 1, or If-None-Match: *
//     for the first manifest. A lost race is [ErrConflict] and changes nothing a reader can see.
//
// Everything is validated with the controlplane Validate functions before the first write. The package never
// lists and never deletes: old objects stay until a later garbage collection. It imports no internal package, so
// an external control plane can use it with any client that has the three calls of [API] (a *s3.Client does).
package s3publish
