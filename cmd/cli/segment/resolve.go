package segment

import (
	"context"
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
	// ErrAmbiguous is returned for a name that two segments documents of the control plane define.
	ErrAmbiguous = errors.New("more than one segments document defines this segment")
)

// resolved is everything that the definition and the bootstrap file say about a segment.
type resolved struct {
	cfg      *bootstrap.Config
	conn     *bootstrap.Connection
	target   *segmentadmin.Target
	connName string
	secrets  bootstrap.LoadOptions
}

// namedDoc is a segments document and where it came from, for the messages.
type namedDoc struct {
	doc   *controlplane.SegmentsDocument
	label string
}

// resolve reads the bootstrap file and the segments document (a file, or the segments objects that the manifest of the
// control plane lists) and returns the target: key, layout and version key come from the definition, the key prefix
// from the connection, the normalization and the longest id from the resolver settings of the bootstrap file, as the
// relay reads them.
func resolve(ctx context.Context, com *Common, deps *Deps) (*resolved, error) {
	secrets := bootstrap.LoadOptions{LookupEnv: deps.LookupEnv, ReadFile: deps.ReadFile, SkipRelayChecks: true}

	cfg, err := bootstrap.Load(&bootstrap.Overrides{Config: com.Config}, secrets)
	if err != nil {
		return nil, fmt.Errorf("bootstrap file: %w", err)
	}

	docs, err := loadDocuments(ctx, com, cfg, secrets, deps)
	if err != nil {
		return nil, err
	}

	def, found, err := findDefinition(docs, com.Segment)
	if err != nil {
		return nil, err
	}

	conn, ok := cfg.Connections[def.Connection]
	if !ok {
		return nil, fmt.Errorf("%s: %q: %w", found.label, def.Connection, ErrNoConnection)
	}

	prefix, err := prefixOf(&conn, def.Connection)
	if err != nil {
		return nil, err
	}

	target := targetOf(cfg, def, prefix)

	err = checkOverlap(found.doc, def, target, cfg, prefix)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", found.label, err)
	}

	return &resolved{cfg: cfg, conn: &conn, target: target, connName: def.Connection, secrets: secrets}, nil
}

// loadDocuments reads the file that --segments names, or, without it, the segments documents of the control plane.
func loadDocuments(
	ctx context.Context, com *Common, cfg *bootstrap.Config, secrets bootstrap.LoadOptions, deps *Deps,
) ([]namedDoc, error) {
	if com.Segments == "" {
		return planeDocuments(ctx, cfg, secrets, deps)
	}

	data, err := deps.ReadFile(com.Segments)
	if err != nil {
		return nil, fmt.Errorf("segments document: %w", err)
	}

	doc, _, err := controlplane.UnmarshalSegments(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", com.Segments, err)
	}

	return []namedDoc{{doc: doc, label: com.Segments}}, nil
}

// checkOverlap refuses a segment whose keys can meet the keys of another segment of the document on the same
// connection (an import removes keys under its own derived names).
func checkOverlap(
	doc *controlplane.SegmentsDocument, def *controlplane.SegmentDefinition, target *segmentadmin.Target,
	cfg *bootstrap.Config, prefix string,
) error {
	for i := range doc.Segments {
		other := &doc.Segments[i]
		if other == def || other.Connection != def.Connection {
			continue
		}

		err := segmentadmin.CheckKeys(target, targetOf(cfg, other, prefix))
		if err != nil {
			return fmt.Errorf("segments[%d] (%s): %w", i, other.Name, err)
		}
	}

	return nil
}

// findDefinition is the dynamic definition of the name, in the one document that defines it.
func findDefinition(docs []namedDoc, name string) (*controlplane.SegmentDefinition, *namedDoc, error) {
	var (
		def   *controlplane.SegmentDefinition
		found *namedDoc
	)

	for i := range docs {
		for pos := range docs[i].doc.Segments {
			if docs[i].doc.Segments[pos].Name != name {
				continue
			}

			if def != nil {
				return nil, nil, fmt.Errorf("%s and %s: %q: %w", found.label, docs[i].label, name, ErrAmbiguous)
			}

			def, found = &docs[i].doc.Segments[pos], &docs[i]
		}
	}

	switch {
	case def == nil:
		return nil, nil, fmt.Errorf("%q: %w", name, ErrNoSegment)
	case def.Connection == "":
		return nil, nil, fmt.Errorf("%s: %q: %w", found.label, name, ErrStatic)
	}

	return def, found, nil
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

	res := cfg.EffectiveResolver()
	target.Normalize = resolver.Normalization(res.Default.Normalize)

	if res.MaxIDBytes != nil {
		target.MaxIDBytes = *res.MaxIDBytes
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
