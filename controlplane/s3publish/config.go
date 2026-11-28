package s3publish

import "time"

// DefaultConcurrency is the number of HeadObject and PutObject calls of objects that run at once.
const DefaultConcurrency = 8

// Config says where a Publisher writes and what it accepts. The zero value of a limit means its default; the
// byte limits default to the ones of the relay reader (controlplane.DefaultMaxObjectBytes, DefaultMaxTotalBytes,
// DefaultMaxManifestBytes), so that what is published is not rejected when it is read.
type Config struct {
	// Check is an optional hook that validates a document beyond the controlplane Validate functions, for example
	// with the engine of the relay (the controlplane/flagcheck package). It runs for every object before any S3
	// call; an error refuses the publication (ErrInvalid) and should name the flag, never a secret. Verify runs
	// it on every object that it read and whose size and checksum match; an error is a ProblemInvalidContent
	// (Object.Source is then the key of the object). The package
	// does not import the engine itself, so an importer only pays for what it uses.
	Check func(obj *Object) error
	// Bucket is the bucket of the connection.
	Bucket string
	// Prefix is put before every key (objects and default manifest key). Empty or a key prefix without leading
	// slash; a missing trailing slash is added. The keys in the manifest are absolute keys of the bucket, prefix
	// included: the reader must not add a prefix.
	Prefix string
	// Channel is the channel of the manifest.
	Channel string
	// Connection is the name of the bootstrap connection of the bucket; it is written in every manifest entry and
	// must be the control-plane connection name of the relay.
	Connection string
	// ManifestKey is the key of the manifest. Default: Prefix + "manifests/" + Channel + ".yaml". A key ending
	// in ".json" is written as JSON.
	ManifestKey string
	// MaxObjectBytes bounds the size of one object (and refuses an empty one). Default 16 MiB.
	MaxObjectBytes int64
	// MaxTotalBytes bounds the sum of the sizes of the distinct objects. Default 1 GiB.
	MaxTotalBytes int64
	// MaxManifestBytes bounds the marshalled manifest. Default 16 MiB.
	MaxManifestBytes int64
	// MaxObjects bounds the objects of a manifest; zero means controlplane.DefaultMaxObjects.
	MaxObjects int
	// Concurrency bounds the object calls that run at once; zero means DefaultConcurrency. The manifest is
	// always written after every object, by itself.
	Concurrency int
}

// Object is one document to publish.
type Object struct {
	// Name is the logical name (^[a-z][a-z0-9-]{0,62}$), unique in the manifest.
	Name string
	// Kind is controlplane.KindFlags, controlplane.KindFlagSets or controlplane.KindSegments.
	Kind string
	// Format is controlplane.FormatYAML or controlplane.FormatJSON.
	Format string
	// Source says where the document came from (a file path), for error messages only. It is not stored.
	Source string
	// Body is the exact bytes to store: the checksum is computed over them.
	Body []byte
}

// Request is one publication.
type Request struct {
	// CreatedAt is written in the manifest (informational); nil omits it.
	CreatedAt *time.Time
	// Objects are the documents of the channel; the manifest lists exactly these.
	Objects []Object
	// Generation is the generation to write. Zero means the generation in the bucket plus one (1 for the first
	// manifest). An explicit value must be higher than the generation in the bucket.
	Generation uint64
	// DryRun computes the plan: it makes the same reads and no write.
	DryRun bool
	// ReplaceInvalidManifest allows the publication to replace a manifest in the bucket that cannot be read, is
	// invalid, or is over the current limits (otherwise ErrPreviousManifest locks the channel). It requires an
	// explicit Generation and still writes with If-Match on the ETag read, so it cannot overwrite a manifest
	// that someone else wrote in between. It has no effect when the manifest in the bucket is valid.
	ReplaceInvalidManifest bool
}
