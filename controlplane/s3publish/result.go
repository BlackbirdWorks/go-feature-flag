package s3publish

// Status is what happened to one object.
type Status string

// The statuses of an object.
const (
	// StatusUploaded: the object was absent and was created.
	StatusUploaded Status = "uploaded"
	// StatusPresent: the object already existed under its key with the expected size.
	StatusPresent Status = "present"
	// StatusWouldUpload: dry run, the object is absent and would be created.
	StatusWouldUpload Status = "would-upload"
)

// ObjectResult is the outcome for one manifest entry.
type ObjectResult struct {
	Name   string
	Key    string
	Status Status
	Size   int64
}

// Requests counts S3 calls by operation.
type Requests struct {
	Get  int
	Head int
	Put  int
}

// Total returns the number of calls.
func (r Requests) Total() int { return r.Get + r.Head + r.Put }

// Result is the outcome of Publish.
type Result struct {
	Channel     string
	ManifestKey string
	// ManifestETag is the ETag of the manifest written, or of the manifest in the bucket when nothing changed.
	ManifestETag string
	// Objects lists every manifest entry in manifest order (sorted by name).
	Objects []ObjectResult
	// Warnings are notes that do not stop the publication, for example unknown fields in a document.
	Warnings []string
	// Requests counts the S3 calls this Publish made.
	Requests Requests
	// Generation is the generation of the manifest written, or in the bucket when nothing changed.
	Generation uint64
	// Previous is the generation found in the bucket, 0 when there was none.
	Previous uint64
	// UploadedBytes is the number of object bytes written (manifest excluded).
	UploadedBytes int64
	// Uploaded and Present count the distinct objects created and found; with DryRun Uploaded counts those that
	// would be created. When the manifest is unchanged, Uploaded counts the listed objects that were missing and
	// were uploaded again.
	Uploaded int
	Present  int
	// Changed reports that the manifest differs from the one in the bucket (a new generation is due).
	Changed bool
	// Written reports that the manifest was written: Changed and not DryRun.
	Written bool
	// DryRun repeats the request.
	DryRun bool
}
