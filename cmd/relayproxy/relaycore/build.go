package relaycore

import (
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/internal/connection"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/internal/manifestreader/statedir"
)

// registerConnections loads the AWS backed connections into the factory, in name order. A file connection is no
// client and is skipped.
func registerConnections(factory *connection.Factory, cfg *bootstrap.Config, opts bootstrap.LoadOptions) error {
	for _, name := range slices.Sorted(maps.Keys(cfg.Connections)) {
		path := "connections." + name

		spec, err := bootstrap.ConnectionSpec(cfg.Connections[name], opts)
		if err != nil {
			return &BuildError{Path: path, Err: err}
		}

		if spec == nil {
			continue
		}

		err = factory.Set(name, spec)
		if err != nil {
			return &BuildError{Path: path, Err: err}
		}
	}

	return nil
}

// objectStores builds the store of every connection that can hold objects: the S3 connections and the file ones.
func objectStores(
	factory *connection.Factory, cfg *bootstrap.Config,
) (map[string]manifestreader.ObjectStore, error) {
	stores := make(map[string]manifestreader.ObjectStore)

	for _, name := range slices.Sorted(maps.Keys(cfg.Connections)) {
		conn := cfg.Connections[name]

		switch {
		case conn.S3 != nil:
			stores[name] = manifestreader.NewS3Store(factory, name, conn.S3.Bucket)
		case conn.File != nil:
			store, err := manifestreader.NewFileStore(conn.File.Path)
			if err != nil {
				return nil, &BuildError{Path: "connections." + name + ".path", Err: err}
			}

			stores[name] = store
		}
	}

	return stores, nil
}

// readerConfig is the configuration of the manifest reader for the controlPlane section.
func readerConfig(plane *bootstrap.ControlPlane) *manifestreader.Config {
	manifest := &plane.Manifest
	rcfg := &manifestreader.Config{
		ManifestKey: manifest.Key,
		Connection:  plane.Connection,
		Channel:     manifest.Channel,
	}

	if manifest.PollInterval != nil {
		rcfg.PollInterval = time.Duration(*manifest.PollInterval)
	}

	if manifest.MaxObjects != nil {
		rcfg.MaxObjects = *manifest.MaxObjects
	}

	// the zero value of the fraction means the default to the reader: an explicit zero turns the jitter off
	if manifest.Jitter != nil {
		rcfg.JitterFraction = *manifest.Jitter
		rcfg.DisableJitter = *manifest.Jitter == 0
	}

	return rcfg
}

// openStateDir opens the state directory of the control plane. An unusable directory never stops the relay: the
// hooks then only report why there is none, and the reason is returned.
func openStateDir(
	plane *bootstrap.ControlPlane, rcfg *manifestreader.Config, deps *Deps, logger *slog.Logger,
) (*statedir.Store, manifestreader.Hooks, string) {
	if plane.StateDir == "" {
		return nil, manifestreader.Hooks{}, ""
	}

	return statedir.OpenOrDisable(statedir.Config{
		FS:               deps.StateFS,
		Logger:           logger,
		Dir:              plane.StateDir,
		MaxObjects:       rcfg.MaxObjects,
		MaxManifestBytes: rcfg.MaxManifestBytes,
		MaxObjectBytes:   rcfg.MaxObjectBytes,
		MaxBytes:         rcfg.MaxTotalBytes,
	})
}
