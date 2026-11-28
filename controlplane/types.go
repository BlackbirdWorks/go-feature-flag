package controlplane

const (
	// APIVersion is the document version this package writes.
	APIVersion = "semaphore/v1"
	// DocumentKindManifest is the kind of a manifest document.
	DocumentKindManifest = "Manifest"
	// DocumentKindFlagSet is the kind of a flag-set document.
	DocumentKindFlagSet = "FlagSet"

	// DocumentKindSegments is the kind of a segments document.
	DocumentKindSegments = "Segments"

	// KindFlags is the manifest object kind of a flag file.
	KindFlags = "flags"
	// KindFlagSets is the manifest object kind of a flag-set document.
	KindFlagSets = "flagSets"
	// KindSegments is the manifest object kind of a segments document.
	KindSegments = "segments"

	// FormatYAML is the YAML text format of an object.
	FormatYAML = "yaml"
	// FormatJSON is the JSON text format of an object.
	FormatJSON = "json"
	// FormatTOML is the TOML text format of an object (flag files only; this package does not read it).
	FormatTOML = "toml"

	// AlgorithmSHA256 is the only checksum algorithm of schema v1.
	AlgorithmSHA256 = "sha256"

	// ObjectVersion1 is the version of the content schema of an object: the headerless flag format.
	ObjectVersion1 = 1
)
