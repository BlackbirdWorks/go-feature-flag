package relaycore

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	kindManifest           = "manifest"
	maxLoggedUnknownFields = 10
)

type unknownTracker struct {
	last map[string]int
	mu   sync.Mutex
}

func (c *Core) noteUnknownFields(ctx context.Context, res *flagruntime.RoundResult) {
	if !res.ContentApplied {
		return
	}

	counts := map[string]int{}
	paths := map[string][]string{}

	manifest := c.reader.Status().Manifest
	if manifest.UnknownFields > 0 {
		counts[kindManifest] = manifest.UnknownFields
		paths[kindManifest] = slices.Clone(
			manifest.UnknownFieldPaths[:min(len(manifest.UnknownFieldPaths), maxLoggedUnknownFields)],
		)
	}

	current := c.holder.Load()
	for _, name := range slices.Sorted(maps.Keys(current.Status.Objects)) {
		obj := current.Status.Objects[name]
		if obj.UnknownFields == 0 {
			continue
		}

		counts[obj.Kind] += obj.UnknownFields
		for _, field := range obj.UnknownFieldPaths {
			if len(paths[obj.Kind]) >= maxLoggedUnknownFields {
				break
			}

			paths[obj.Kind] = append(paths[obj.Kind], obj.Name+"/"+field)
		}
	}

	kinds := []string{controlplane.KindFlagSets, controlplane.KindFlags, kindManifest, controlplane.KindSegments}
	for _, kind := range kinds {
		c.logUnknownFieldsKind(ctx, kind, counts[kind], paths[kind])
	}
}

func (c *Core) logUnknownFieldsKind(ctx context.Context, kind string, count int, fields []string) {
	c.unknownFields.mu.Lock()
	if c.unknownFields.last == nil {
		c.unknownFields.last = map[string]int{}
	}

	prev := c.unknownFields.last[kind]
	c.unknownFields.last[kind] = count
	c.unknownFields.mu.Unlock()

	if count == prev {
		return
	}

	if count > 0 {
		c.log.LogAttrs(ctx, slog.LevelWarn, "unknown fields in control-plane documents",
			slog.String("kind", kind), slog.Int("count", count), slog.Any("fields", fields))

		return
	}

	c.log.LogAttrs(ctx, slog.LevelInfo, "unknown fields cleared in control-plane documents",
		slog.String("kind", kind))
}
