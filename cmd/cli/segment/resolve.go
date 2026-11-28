package segment

import (
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

var (
	// ErrNoSegment is returned for a name that the segments document does not define.
	ErrNoSegment = errors.New("the segments document does not define this segment")
	// ErrStatic is returned for a segment that has no connection: its members are in the document.
	ErrStatic = errors.New("the segment is static (no connection): its members are in the document")
	// ErrNoConnection is returned for a connection that the bootstrap file does not define.
	ErrNoConnection = errors.New("the bootstrap file does not define the connection of the segment")
	// ErrNotStore is returned for a connection that is neither valkey nor redis.
	ErrNotStore = errors.New("the connection of the segment is not a valkey or redis connection")
)

// resolved is everything that the definition and the bootstrap file say about a segment.
type resolved struct {
	conn     *bootstrap.Connection
	target   *segmentadmin.Target
	connName string
	secrets  bootstrap.LoadOptions
}

// resolve reads the bootstrap file and the segments document and returns the target: key, layout and version key
// come from the definition, the key prefix from the connection, the normalization and the longest id from the
// resolver settings of the bootstrap file, as the relay reads them.
func resolve(com *Common, deps *Deps) (*resolved, error) {
	secrets := bootstrap.LoadOptions{LookupEnv: deps.LookupEnv, ReadFile: deps.ReadFile, SkipRelayChecks: true}

	cfg, err := bootstrap.Load(&bootstrap.Overrides{Config: com.Config}, secrets)
	if err != nil {
		return nil, fmt.Errorf("bootstrap file: %w", err)
	}

	data, err := deps.ReadFile(com.Segments)
	if err != nil {
		return nil, fmt.Errorf("segments document: %w", err)
	}

	doc, _, err := controlplane.UnmarshalSegments(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", com.Segments, err)
	}

	def, err := findDefinition(doc, com)
	if err != nil {
		return nil, err
	}

	conn, ok := cfg.Connections[def.Connection]
	if !ok {
		return nil, fmt.Errorf("%q: %w", def.Connection, ErrNoConnection)
	}

	prefix, err := prefixOf(&conn, def.Connection)
	if err != nil {
		return nil, err
	}

	return &resolved{
		conn: &conn, target: targetOf(cfg, def, prefix), connName: def.Connection, secrets: secrets,
	}, nil
}

// findDefinition is the dynamic definition that the flags name.
func findDefinition(doc *controlplane.SegmentsDocument, com *Common) (*controlplane.SegmentDefinition, error) {
	for i := range doc.Segments {
		def := &doc.Segments[i]
		if def.Name != com.Segment {
			continue
		}

		if def.Connection == "" {
			return nil, fmt.Errorf("%s: %q: %w", com.Segments, com.Segment, ErrStatic)
		}

		return def, nil
	}

	return nil, fmt.Errorf("%s: %q: %w", com.Segments, com.Segment, ErrNoSegment)
}

// targetOf is the segment as the relay reads it.
func targetOf(cfg *bootstrap.Config, def *controlplane.SegmentDefinition, prefix string) *segmentadmin.Target {
	layout := redissegment.Layout(def.Layout)
	if layout == "" {
		layout = redissegment.LayoutSet
	}

	target := &segmentadmin.Target{Name: def.Name, Key: def.Key, Layout: layout, Normalize: resolver.NormalizeNone}
	if target.Key == "" {
		target.Key = redissegment.DefaultKey(prefix, def.Name, layout)
	}

	if def.Version != nil {
		target.VersionKey = def.Version.Key
	}

	if cfg.Segments != nil {
		res := &cfg.Segments.Resolver
		target.Normalize = resolver.Normalization(res.Default.Normalize)

		if res.MaxIDBytes != nil {
			target.MaxIDBytes = *res.MaxIDBytes
		}
	}

	return target
}

// prefixOf is the key prefix of a store connection.
func prefixOf(conn *bootstrap.Connection, name string) (string, error) {
	switch {
	case conn.Valkey != nil:
		return conn.Valkey.KeyPrefix, nil
	case conn.Redis != nil:
		return conn.Redis.KeyPrefix, nil
	}

	return "", fmt.Errorf("%q: %w", name, ErrNotStore)
}
