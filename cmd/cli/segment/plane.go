package segment

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

var (
	// ErrNoControlPlane is returned when --segments is not given and the bootstrap file has no controlPlane section.
	ErrNoControlPlane = errors.New("give --segments, or a bootstrap file with a controlPlane section")
	// ErrNoStore is returned for a connection of the control plane that cannot hold objects.
	ErrNoStore = errors.New("the connection holds no objects (it must be s3 or file)")
	// ErrNoSegmentsObject is returned when the manifest lists no segments document.
	ErrNoSegmentsObject = errors.New("the manifest lists no segments object")
)

// planeDocuments reads the segments documents that the manifest of the control plane lists, with the object stores
// of the relay: the manifest, then each segments object, whose size and checksum must be the listed ones.
func planeDocuments(
	ctx context.Context, cfg *bootstrap.Config, secrets bootstrap.LoadOptions, deps *Deps,
) ([]namedDoc, error) {
	plane := cfg.ControlPlane
	if plane == nil {
		return nil, ErrNoControlPlane
	}

	stores, err := objectStores(cfg, secrets, deps)
	if err != nil {
		return nil, err
	}

	store, ok := stores[plane.Connection]
	if !ok {
		return nil, fmt.Errorf("controlPlane.connection %q: %w", plane.Connection, ErrNoStore)
	}

	raw, err := store.Get(ctx, manifestreader.GetRequest{
		Key: plane.Manifest.Key, Limit: manifestreader.DefaultMaxManifestBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("controlPlane.manifest.key %q: %w", plane.Manifest.Key, err)
	}

	manifest, _, err := controlplane.UnmarshalManifest(raw.Bytes)
	if err != nil {
		return nil, fmt.Errorf("manifest %q: %w", plane.Manifest.Key, err)
	}

	var docs []namedDoc

	for i := range manifest.Objects {
		obj := &manifest.Objects[i]
		if obj.Kind != controlplane.KindSegments {
			continue
		}

		doc, objErr := segmentsObject(ctx, stores, obj)
		if objErr != nil {
			return nil, fmt.Errorf("manifest %q: objects[%d] (%s): %w", plane.Manifest.Key, i, obj.Name, objErr)
		}

		docs = append(docs, namedDoc{doc: doc, label: "segments object " + obj.Name})
	}

	if len(docs) == 0 {
		return nil, fmt.Errorf("manifest %q: %w", plane.Manifest.Key, ErrNoSegmentsObject)
	}

	return docs, nil
}

func segmentsObject(
	ctx context.Context, stores map[string]manifestreader.ObjectStore, obj *controlplane.ManifestObject,
) (*controlplane.SegmentsDocument, error) {
	store, ok := stores[obj.Connection]
	if !ok {
		return nil, fmt.Errorf("connection %q: %w", obj.Connection, ErrNoStore)
	}

	got, err := store.Get(ctx, manifestreader.GetRequest{
		Key: obj.Key, VersionID: obj.VersionID, Limit: min(obj.Size, manifestreader.DefaultMaxObjectBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("key %q: %w", obj.Key, err)
	}

	err = controlplane.VerifyChecksum(got.Bytes, obj.Checksum)
	if err != nil {
		return nil, fmt.Errorf("key %q: %w", obj.Key, err)
	}

	doc, _, err := controlplane.UnmarshalSegments(got.Bytes)
	if err != nil {
		return nil, fmt.Errorf("key %q: %w", obj.Key, err)
	}

	return doc, nil
}

// objectStores builds the store of each s3 and file connection of the bootstrap file. The S3 clients come from the one
// connection factory of the relay.
func objectStores(
	cfg *bootstrap.Config, secrets bootstrap.LoadOptions, deps *Deps,
) (map[string]manifestreader.ObjectStore, error) {
	factory := connection.New(deps.ConnectionOptions...)
	stores := map[string]manifestreader.ObjectStore{}

	for _, name := range slices.Sorted(maps.Keys(cfg.Connections)) {
		conn := cfg.Connections[name]

		switch {
		case conn.S3 != nil:
			spec, err := bootstrap.S3Spec(conn.S3, secrets)
			if err == nil {
				err = factory.Set(name, spec)
			}

			if err != nil {
				return nil, fmt.Errorf("connections.%s: %w", name, err)
			}

			stores[name] = manifestreader.NewS3Store(factory, name, conn.S3.Bucket)
		case conn.File != nil:
			store, err := manifestreader.NewFileStore(conn.File.Path)
			if err != nil {
				return nil, fmt.Errorf("connections.%s.path: %w", name, err)
			}

			stores[name] = store
		}
	}

	return stores, nil
}
