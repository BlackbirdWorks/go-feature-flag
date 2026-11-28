package flagruntime

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

func capUnknownFields(paths []string) []string {
	return manifestreader.CapUnknownFieldPaths(paths)
}

// parsedFlags is the result of parsing one flags object. It is cached by checksum and never changed.
type parsedFlags struct {
	err           error
	valid         map[string]*flagdef.InternalFlag
	hashes        map[string]uint64
	refs          map[string][]string // the names of the segments that a flag uses, only for the flags that use some
	rejected      []rejection
	unknownFields []string
	unknownCount  int
}

type rejection struct {
	flag   string
	reason string
}

// parsedDoc is the result of decoding one flag-set document. It is cached by checksum and never changed.
type parsedDoc struct {
	err           error
	doc           *controlplane.FlagSetDocument
	unknownFields []string
	unknownCount  int
}

// parseFlags decodes a flags object with the engine parser and validates every flag with the shared service.
func parseFlags(svc *flagdef.Service, obj *Object) *parsedFlags {
	sum := obj.checksum()

	// the parser of the engine, as the version 1 relay calls it: nothing is decoded or rewritten here
	raw, err := cache.ConvertToFlagStruct(obj.Bytes, obj.Format)
	if err != nil {
		return &parsedFlags{err: fmt.Errorf("%w: %w", ErrDecode, err)}
	}

	names := make([]string, 0, len(raw))
	for name := range raw {
		names = append(names, name)
	}

	slices.Sort(names)

	out := &parsedFlags{
		valid:  make(map[string]*flagdef.InternalFlag, len(raw)),
		hashes: make(map[string]uint64, len(raw)),
		refs:   map[string][]string{},
	}

	if obj.Format != controlplane.FormatTOML {
		_, info, uerr := controlplane.UnmarshalFlags(obj.Bytes, obj.Format)
		if uerr == nil && len(info.UnknownFields) > 0 {
			out.unknownCount = len(info.UnknownFields)
			out.unknownFields = capUnknownFields(info.UnknownFields)
		}
	}

	for _, name := range names {
		def := raw[name]
		converted := def.Convert()

		verr := converted.IsValid(svc)
		if verr != nil {
			out.rejected = append(out.rejected, rejection{flag: name, reason: verr.Error()})

			continue
		}

		out.valid[name] = &converted
		out.hashes[name] = contentHash(&converted, sum, name)

		if used := converted.SegmentNames(svc); len(used) > 0 {
			out.refs[name] = used
		}
	}

	return out
}

// parseDoc decodes a flag-set document.
func parseDoc(obj *Object) *parsedDoc {
	doc, info, err := controlplane.UnmarshalFlagSet(obj.Bytes)
	if err != nil {
		return &parsedDoc{err: fmt.Errorf("%w: %w", ErrDecode, err)}
	}

	return &parsedDoc{
		doc:           doc,
		unknownFields: capUnknownFields(info.UnknownFields),
		unknownCount:  len(info.UnknownFields),
	}
}

// contentHash hashes the canonical form of a parsed flag: the JSON encoding of the flag, in which map keys are
// sorted. It does not depend on the bytes the flag was read from (format, comments, key order). A flag that has
// no JSON encoding (for example a NaN in its metadata) gets a hash of the checksum of its object and of its key:
// it may report a change that is not one, and never misses a change.
func contentHash(fl *flagdef.InternalFlag, checksum, key string) uint64 {
	data, err := json.Marshal(fl)
	if err != nil {
		return mixString(mixString(fnvOffset, checksum), key)
	}

	return hashBytes(data)
}

// tables is the effective flag table of a source object.
type tables struct {
	flags    map[string]*flagdef.InternalFlag
	hashes   map[string]uint64
	refs     map[string][]string
	rejected []FlagRejection
}

// copyRef copies the segments that a flag uses from one table to another, when it uses some.
func copyRef(dst, src map[string][]string, flag string) {
	if names, ok := src[flag]; ok {
		dst[flag] = names
	}
}

// buildTable merges a parse result with the previous table of the same object (last-known-good per flag):
// a flag that failed validation keeps its previous definition, and a flag whose definition did not change
// keeps its previous pointer, so unchanged flags exist once across versions. fresh says that parsed was parsed
// for this object just now (nobody else holds its map), so it may be updated to the kept pointers.
func buildTable(parsed *parsedFlags, prev *objState, fresh bool) tables {
	rejected := make([]FlagRejection, 0, len(parsed.rejected))

	hasPrev := prev != nil && prev.loaded
	if !hasPrev && len(parsed.rejected) == 0 {
		return tables{flags: parsed.valid, hashes: parsed.hashes, refs: parsed.refs, rejected: rejected}
	}

	flags := make(map[string]*flagdef.InternalFlag, len(parsed.valid)+len(parsed.rejected))
	hashes := make(map[string]uint64, len(parsed.valid)+len(parsed.rejected))
	refs := maps.Clone(parsed.refs)

	for key, candidate := range parsed.valid {
		flags[key], hashes[key] = candidate, parsed.hashes[key]

		if hasPrev {
			if old, ok := prev.flags[key]; ok && reflect.DeepEqual(old, candidate) {
				flags[key], hashes[key] = old, prev.hashes[key]

				if fresh {
					// the parse result is not shared yet: it keeps the running pointer too, so the flag is held once
					parsed.valid[key] = old
				}
			}
		}
	}

	for _, rej := range parsed.rejected {
		report := FlagRejection{Flag: rej.flag, Reason: rej.reason}

		if hasPrev {
			if old, ok := prev.flags[rej.flag]; ok {
				flags[rej.flag], hashes[rej.flag] = old, prev.hashes[rej.flag]
				report.Kept = true

				copyRef(refs, prev.refs, rej.flag)
			}
		}

		rejected = append(rejected, report)
	}

	return tables{flags: flags, hashes: hashes, refs: refs, rejected: rejected}
}
