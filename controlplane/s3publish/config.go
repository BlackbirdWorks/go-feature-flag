package s3publish

import "time"

// Config says where a Publisher writes.
type Config struct {
	// Bucket is the bucket of the connection.
	Bucket string
	// Prefix is put before every key (objects and default manifest key). Empty or a key prefix without leading
	// slash; a missing trailing slash is added.
	Prefix string
	// Channel is the channel of the manifest.
	Channel string
	// Connection is the name of the bootstrap connection of the bucket; it is written in every manifest entry.
	Connection string
	// ManifestKey is the key of the manifest. Default: Prefix + "manifests/" + Channel + ".yaml". A key ending
	// in ".json" is written as JSON, any other as YAML.
	ManifestKey string
	// MaxObjects bounds the objects of a manifest; zero means controlplane.DefaultMaxObjects.
	MaxObjects int
}

// Object is one document to publish.
type Object struct {
	// Name is the logical name (^[a-z][a-z0-9-]{0,62}$), unique in the manifest.
	Name string
	// Kind is controlplane.KindFlags or controlplane.KindFlagSets.
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
}
