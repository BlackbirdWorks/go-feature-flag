package controlplane

import (
	"strconv"
	"time"
)

// The limits of a segments document and of the segments in it.
const (
	// MaxSegmentMembers is the most members of a static segment.
	MaxSegmentMembers = 100_000
	// MaxSegmentMemberBytes is the most bytes of the members of one static segment (the sum of their lengths).
	MaxSegmentMemberBytes = 4 << 20
	// MaxSegmentMemberLen is the longest member of a static segment.
	MaxSegmentMemberLen = 4096
	// MaxSegmentNameLen is the longest name of a segment.
	MaxSegmentNameLen = 64
	// MaxSegmentKeyLen is the longest key or version of a dynamic segment.
	MaxSegmentKeyLen = 512
	// MinVersionInterval is the shortest poll interval of a version key.
	MinVersionInterval = 100 * time.Millisecond
	// MaxSegmentDuration is the longest interval or time-to-live of a segment.
	MaxSegmentDuration = 24 * time.Hour
	// MaxCacheEntries is the most cache entries that a lookup segment may ask for.
	MaxCacheEntries = 10_000_000
)

// The values of the fields of a segment.
const (
	// SegmentModeSnapshot pulls the whole member set and keeps it in memory. It is the default.
	SegmentModeSnapshot = "snapshot"
	// SegmentModeLookup asks the store for each evaluation.
	SegmentModeLookup = "lookup"
	// SegmentLayoutSet stores the members in one set.
	SegmentLayoutSet = "set"
	// SegmentLayoutKeys stores one key per member (lookup only).
	SegmentLayoutKeys = "keys"
	// SegmentMissingNotMember: a segment that does not exist has no member. It is the default.
	SegmentMissingNotMember = "notMember"
	// SegmentMissingUnknown: a segment that does not exist is unknown.
	SegmentMissingUnknown = "unknown"
)

// MemberList is the member list of a static segment. It is a list of strings like any other, except that the decoder
// rejects an item that is null or is not a string (it would otherwise drop the first and turn the other into text).
type MemberList []string

// SegmentsDocument is the document of kind Segments: named segment definitions.
type SegmentsDocument struct {
	// APIVersion is the document version, "semaphore/v1". Empty means the current version when marshalling.
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	// Kind is "Segments". Empty means "Segments" when marshalling.
	Kind string `json:"kind" yaml:"kind"`
	// Segments are the definitions.
	Segments []SegmentDefinition `json:"segments" yaml:"segments"`
}

// SegmentDefinition is one segment. It has no type: with a Connection it is a dynamic segment (its members are in
// a store, and the store kind is the one of the connection of the relay bootstrap file), without one it is a static
// segment with its Members inline. Exactly one of the two must be given.
type SegmentDefinition struct { //nolint:govet // fieldalignment: the field order is the wire order
	// Name is the name that rules use: segment("name").
	Name string `json:"name" yaml:"name"`
	// Connection names a connection of the relay bootstrap file: a dynamic segment.
	Connection string `json:"connection,omitempty" yaml:"connection,omitempty"`
	// Members are the members of a static segment. An empty list is a static segment with no member.
	Members MemberList `json:"members,omitempty" yaml:"members,omitempty"`
	// Mode is "snapshot" (default) or "lookup" (dynamic only).
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	// Key is the key of the set (layout set) or the prefix of the member keys (layout keys) in the store.
	Key string `json:"key,omitempty" yaml:"key,omitempty"`
	// Layout is "set" (default) or "keys". The keys layout is lookup only.
	Layout string `json:"layout,omitempty" yaml:"layout,omitempty"`
	// MissingKey is "notMember" (default) or "unknown": what a segment that does not exist in the store means.
	MissingKey string `json:"missingKey,omitempty" yaml:"missingKey,omitempty"`
	// OnError is what a lookup that could not be answered becomes: "notMember" (default), "unknown" or "member".
	OnError string `json:"onError,omitempty" yaml:"onError,omitempty"`
	// Version says how the version of the segment is learned (dynamic only).
	Version *SegmentVersion `json:"version,omitempty" yaml:"version,omitempty"`
	// Cache sizes the cache of a lookup segment.
	Cache *SegmentCache `json:"cache,omitempty" yaml:"cache,omitempty"`
}

// SegmentVersion is the version of a dynamic segment: the publisher bumps Value, or the writer of the members
// bumps the version key Key in the store, which the relay polls every Interval. Value and Key are exclusive.
type SegmentVersion struct {
	// Value is a version that the document carries.
	Value string `json:"value,omitempty" yaml:"value,omitempty"`
	// Key is the version key in the store.
	Key string `json:"key,omitempty" yaml:"key,omitempty"`
	// Interval is the poll period of Key, a duration such as "5s" (needs Key).
	Interval string `json:"interval,omitempty" yaml:"interval,omitempty"`
	// Debounce is how long, at most, a refresh of a snapshot segment waits for a set that changes while it is pulled,
	// a duration such as "5s" (needs Key; snapshot mode only). Empty or "0s" is off, the default: the first pull is
	// installed.
	Debounce string `json:"debounce,omitempty" yaml:"debounce,omitempty"`
}

// SegmentCache sizes the cache of a lookup segment.
type SegmentCache struct { //nolint:govet // fieldalignment: the field order is the wire order
	// MaxEntries is the most cached answers.
	MaxEntries int `json:"maxEntries,omitempty" yaml:"maxEntries,omitempty"`
	// TTL is how long an answer is cached, a duration such as "30s".
	TTL string `json:"ttl,omitempty" yaml:"ttl,omitempty"`
}

// UnmarshalSegments reads a segments document from YAML or JSON. See UnmarshalManifest.
func UnmarshalSegments(data []byte) (*SegmentsDocument, DecodeInfo, error) {
	var doc SegmentsDocument

	info, err := decodeDocument(data, DocumentKindSegments, &doc)
	if err != nil {
		return nil, DecodeInfo{}, err
	}

	return &doc, info, nil
}

// MarshalSegments writes d as deterministic YAML. See MarshalManifest for the header rules.
func MarshalSegments(d *SegmentsDocument) ([]byte, error) {
	doc, err := headedSegments(d)
	if err != nil {
		return nil, err
	}

	return marshal(doc, writeYAML)
}

// MarshalSegmentsJSON writes d as deterministic indented JSON.
func MarshalSegmentsJSON(d *SegmentsDocument) ([]byte, error) {
	doc, err := headedSegments(d)
	if err != nil {
		return nil, err
	}

	return marshal(doc, writeJSON)
}

// headedSegments returns a copy of d with the header filled in.
func headedSegments(d *SegmentsDocument) (*SegmentsDocument, error) {
	if d == nil {
		return nil, ErrNilDocument
	}

	doc := *d

	err := fillHeader(&doc.APIVersion, &doc.Kind, DocumentKindSegments)
	if err != nil {
		return nil, err
	}

	return &doc, nil
}

// ValidateSegments checks a segments document (an empty header means the current one). Header failures are returned
// as for ValidateManifest; the other problems are a *ValidationError whose problems have Kind EntrySegment, so a
// caller can keep the good segments. Of two segments with one name the first keeps it. It mirrors the validation of
// the engine's segment definition; the parity test of the flagcheck package keeps the two together.
func ValidateSegments(doc *SegmentsDocument) error {
	if doc == nil {
		return ErrNilDocument
	}

	err := checkHeader(orDefault(doc.APIVersion, APIVersion), orDefault(doc.Kind, DocumentKindSegments),
		DocumentKindSegments)
	if err != nil {
		return err
	}

	var probs problems

	seen := map[string]string{}

	for i := range doc.Segments {
		seg := &doc.Segments[i]
		owner := entry{kind: EntrySegment, name: seg.Name, index: i}
		path := "segments[" + strconv.Itoa(i) + "]"

		if !validSegmentName(seg.Name) {
			probs.addf(owner, CodeInvalidName, path+".name",
				"%q is not 1 to 64 characters of a-z, 0-9, _ and -", seg.Name)
		}

		if first, dup := seen[seg.Name]; dup {
			probs.addf(owner, CodeDuplicateName, path+".name", "duplicate name %q, first used at %s", seg.Name, first)
		} else {
			seen[seg.Name] = path + ".name"
		}

		validateSegment(&probs, owner, path, seg)
	}

	return probs.err()
}

func validSegmentName(name string) bool {
	if name == "" || len(name) > MaxSegmentNameLen {
		return false
	}

	for i := range len(name) {
		c := name[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' && c != '-' {
			return false
		}
	}

	return true
}

func validateSegment(probs *problems, owner entry, path string, seg *SegmentDefinition) {
	switch seg.OnError {
	case "", "unknown", SegmentMissingNotMember, "member":
	default:
		probs.addf(owner, CodeInvalidValue, path+".onError", "%q is not unknown, notMember or member", seg.OnError)
	}

	static := seg.Members != nil

	switch {
	case seg.Connection != "" && static:
		probs.addf(owner, CodeInvalidValue, path, "has both a connection (dynamic) and members (static): give one")
	case seg.Connection == "" && !static:
		probs.addf(owner, CodeMissingValue, path, "needs a connection (dynamic segment) or members (static segment)")
	case static:
		validateStatic(probs, owner, path, seg)
	default:
		validateDynamic(probs, owner, path, seg)
	}
}

func validateStatic(probs *problems, owner entry, path string, seg *SegmentDefinition) {
	for _, field := range []struct {
		name string
		set  bool
	}{
		{"mode", seg.Mode != ""}, {"key", seg.Key != ""}, {"layout", seg.Layout != ""},
		{"missingKey", seg.MissingKey != ""}, {"version", seg.Version != nil}, {"cache", seg.Cache != nil},
	} {
		if field.set {
			probs.addf(
				owner,
				CodeInvalidValue,
				path+"."+field.name,
				"is for a dynamic segment: a static segment has members",
			)
		}
	}

	if len(seg.Members) > MaxSegmentMembers {
		probs.addf(
			owner,
			CodeInvalidValue,
			path+".members",
			"has %d members, more than %d",
			len(seg.Members),
			MaxSegmentMembers,
		)
	}

	seen := make(map[string]struct{}, len(seg.Members))
	total := 0

	for i, member := range seg.Members {
		memberPath := path + ".members[" + strconv.Itoa(i) + "]"

		switch {
		case member == "":
			probs.addf(owner, CodeInvalidValue, memberPath, "is empty")
		case len(member) > MaxSegmentMemberLen:
			probs.addf(owner, CodeInvalidValue, memberPath, "is longer than %d bytes", MaxSegmentMemberLen)
		}

		if _, dup := seen[member]; dup {
			probs.addf(owner, CodeDuplicateKey, memberPath, "is a duplicate of an earlier member")
		}

		seen[member] = struct{}{}
		total += len(member)
	}

	if total > MaxSegmentMemberBytes {
		probs.addf(owner, CodeInvalidValue, path+".members", "has %d bytes, more than %d", total, MaxSegmentMemberBytes)
	}
}

func validateDynamic(probs *problems, owner entry, path string, seg *SegmentDefinition) {
	if !validName(seg.Connection) {
		probs.addf(
			owner,
			CodeInvalidName,
			path+".connection",
			"%q does not match ^[a-z][a-z0-9-]{0,62}$",
			seg.Connection,
		)
	}

	mode := seg.Mode

	switch mode {
	case "":
		mode = SegmentModeSnapshot
	case SegmentModeSnapshot, SegmentModeLookup:
	default:
		probs.addf(owner, CodeInvalidValue, path+".mode", "%q is not snapshot or lookup", seg.Mode)
	}

	switch seg.Layout {
	case "", SegmentLayoutSet:
	case SegmentLayoutKeys:
		if mode == SegmentModeSnapshot {
			probs.addf(owner, CodeInvalidValue, path+".layout",
				"segment %q: layout %q with mode %q: the keys layout is lookup-only", seg.Name, seg.Layout, mode)
		}
	default:
		probs.addf(owner, CodeInvalidValue, path+".layout", "%q is not set or keys", seg.Layout)
	}

	switch seg.MissingKey {
	case "", SegmentMissingNotMember, SegmentMissingUnknown:
	default:
		probs.addf(owner, CodeInvalidValue, path+".missingKey", "%q is not notMember or unknown", seg.MissingKey)
	}

	if len(seg.Key) > MaxSegmentKeyLen {
		probs.addf(owner, CodeInvalidValue, path+".key", "is longer than %d bytes", MaxSegmentKeyLen)
	}

	validateVersion(probs, owner, path+".version", seg.Version, mode)
	validateCache(probs, owner, path+".cache", seg.Cache, mode)
}

func validateVersion(probs *problems, owner entry, path string, version *SegmentVersion, mode string) {
	if version == nil {
		return
	}

	if version.Value != "" && version.Key != "" {
		probs.addf(owner, CodeInvalidValue, path, "value and key are mutually exclusive")
	}

	if len(version.Value) > MaxSegmentKeyLen || len(version.Key) > MaxSegmentKeyLen {
		probs.addf(owner, CodeInvalidValue, path, "value or key is longer than %d bytes", MaxSegmentKeyLen)
	}

	validateDebounce(probs, owner, path+".debounce", version, mode)

	if version.Interval == "" {
		return
	}

	if version.Key == "" {
		probs.addf(owner, CodeInvalidValue, path+".interval", "needs a key")

		return
	}

	interval, err := time.ParseDuration(version.Interval)

	switch {
	case err != nil:
		probs.addf(owner, CodeInvalidValue, path+".interval", "is not a duration such as 5s")
	case interval < MinVersionInterval || interval > MaxSegmentDuration:
		probs.addf(
			owner,
			CodeInvalidValue,
			path+".interval",
			"must be between %s and %s",
			MinVersionInterval,
			MaxSegmentDuration,
		)
	}
}

func validateDebounce(probs *problems, owner entry, path string, version *SegmentVersion, mode string) {
	if version.Debounce == "" {
		return
	}

	debounce, err := time.ParseDuration(version.Debounce)

	switch {
	case err != nil:
		probs.addf(owner, CodeInvalidValue, path, "is not a duration such as 5s")
	case debounce < 0 || debounce > MaxSegmentDuration:
		probs.addf(owner, CodeInvalidValue, path, "must be between 0 and %s", MaxSegmentDuration)
	case debounce == 0:
		// off, the default: valid for any mode
	case mode != SegmentModeSnapshot:
		probs.addf(owner, CodeInvalidValue, path, "is for the snapshot mode only")
	case version.Key == "":
		probs.addf(owner, CodeInvalidValue, path, "needs a key")
	}
}

func validateCache(probs *problems, owner entry, path string, cache *SegmentCache, mode string) {
	if cache == nil {
		return
	}

	if mode != SegmentModeLookup {
		probs.addf(owner, CodeInvalidValue, path, "is for the lookup mode only")
	}

	if cache.MaxEntries < 0 || cache.MaxEntries > MaxCacheEntries {
		probs.addf(owner, CodeInvalidValue, path+".maxEntries", "must be between 0 and %d", MaxCacheEntries)
	}

	if cache.TTL == "" {
		return
	}

	ttl, err := time.ParseDuration(cache.TTL)

	switch {
	case err != nil:
		probs.addf(owner, CodeInvalidValue, path+".ttl", "is not a duration such as 30s")
	case ttl <= 0 || ttl > MaxSegmentDuration:
		probs.addf(owner, CodeInvalidValue, path+".ttl", "must be above 0 and at most %s", MaxSegmentDuration)
	}
}
