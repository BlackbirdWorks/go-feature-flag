package controlplane

import "time"

// Manifest lists the objects of one channel at one generation. It is the pointer to the current state and is
// written last by a publisher.
type Manifest struct { //nolint:govet // fieldalignment: the field order is the wire order
	// APIVersion is the document version, "semaphore/v1". Empty means the current version when marshalling.
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	// Kind is "Manifest". Empty means "Manifest" when marshalling.
	Kind string `json:"kind" yaml:"kind"`
	// Channel names the channel (environment) of the manifest.
	Channel string `json:"channel" yaml:"channel"`
	// Generation increases strictly with every publication of a channel, starting at 1.
	Generation uint64 `json:"generation" yaml:"generation"`
	// CreatedAt is informational.
	CreatedAt *time.Time `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
	// Objects are the source objects.
	Objects []ManifestObject `json:"objects" yaml:"objects"`
}

// ManifestObject is one source object of a manifest.
type ManifestObject struct { //nolint:govet // fieldalignment: the field order is the wire order
	// Name is the logical name, unique in the manifest; flag sets reference objects by it.
	Name string `json:"name" yaml:"name"`
	// Kind is KindFlags, KindFlagSets or KindSegments.
	Kind string `json:"kind" yaml:"kind"`
	// Connection names a connection of the relay bootstrap file; it fixes endpoint, bucket and credentials.
	Connection string `json:"connection" yaml:"connection"`
	// Key is the key of the object in the bucket of the connection.
	Key string `json:"key" yaml:"key"`
	// Format is FormatYAML, FormatJSON or (flags only) FormatTOML.
	Format string `json:"format" yaml:"format"`
	// Version is the version of the content schema; 0 means ObjectVersion1.
	Version int `json:"version,omitempty" yaml:"version,omitempty"`
	// Checksum is the checksum of the exact stored bytes.
	Checksum Checksum `json:"checksum" yaml:"checksum"`
	// Size is the size of the stored bytes.
	Size int64 `json:"size" yaml:"size"`
	// VersionID is an optional object version id of the store.
	VersionID string `json:"versionId,omitempty" yaml:"versionId,omitempty"`
}

// Checksum is a content checksum: an algorithm and a lower-case hex value.
type Checksum struct {
	// Algorithm is AlgorithmSHA256.
	Algorithm string `json:"algorithm" yaml:"algorithm"`
	// Value is the lower-case hex digest.
	Value string `json:"value" yaml:"value"`
}
