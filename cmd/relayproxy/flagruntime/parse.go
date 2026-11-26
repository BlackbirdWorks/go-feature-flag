package flagruntime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// parsedFlags is the result of parsing one flags object. It is cached by checksum and never changed.
type parsedFlags struct {
	err      error
	valid    map[string]*flagdef.InternalFlag
	hashes   map[string]uint64
	rejected []rejection
}

type rejection struct {
	flag   string
	reason string
}

// parsedDoc is the result of decoding one flag-set document. It is cached by checksum and never changed.
type parsedDoc struct {
	err error
	doc *controlplane.FlagSetDocument
}

// parseFlags decodes a flags object with the engine parser and validates every flag with the shared service.
func parseFlags(svc *flagdef.Service, obj *Object) *parsedFlags {
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
		out.hashes[name] = contentHash(&converted)
	}

	return out
}

// parseDoc decodes a flag-set document.
func parseDoc(obj *Object) *parsedDoc {
	doc, _, err := controlplane.UnmarshalFlagSet(obj.Bytes)
	if err != nil {
		return &parsedDoc{err: fmt.Errorf("%w: %w", ErrDecode, err)}
	}

	return &parsedDoc{doc: doc}
}

// contentHash hashes the canonical form of a parsed flag: the JSON encoding of the flag, in which map keys are
// sorted. It does not depend on the bytes the flag was read from (format, comments, key order).
func contentHash(fl *flagdef.InternalFlag) uint64 {
	data, err := json.Marshal(fl)
	if err != nil {
		return hashString(err.Error())
	}

	return hashBytes(data)
}

// tables is the effective flag table of a source object.
type tables struct {
	flags    map[string]*flagdef.InternalFlag
	hashes   map[string]uint64
	rejected []FlagRejection
}

// buildTable merges a parse result with the previous table of the same object (last-known-good per flag):
// a flag that failed validation keeps its previous definition, and a flag whose definition did not change
// keeps its previous pointer, so unchanged flags exist once across versions.
func buildTable(parsed *parsedFlags, prev *objState) tables {
	rejected := make([]FlagRejection, 0, len(parsed.rejected))

	hasPrev := prev != nil && prev.loaded
	if !hasPrev && len(parsed.rejected) == 0 {
		return tables{flags: parsed.valid, hashes: parsed.hashes, rejected: rejected}
	}

	flags := make(map[string]*flagdef.InternalFlag, len(parsed.valid)+len(parsed.rejected))
	hashes := make(map[string]uint64, len(parsed.valid)+len(parsed.rejected))

	for key, fresh := range parsed.valid {
		flags[key], hashes[key] = fresh, parsed.hashes[key]

		if hasPrev {
			if old, ok := prev.flags[key]; ok && reflect.DeepEqual(old, fresh) {
				flags[key], hashes[key] = old, prev.hashes[key]
			}
		}
	}

	for _, rej := range parsed.rejected {
		report := FlagRejection{Flag: rej.flag, Reason: rej.reason}

		if hasPrev {
			if old, ok := prev.flags[rej.flag]; ok {
				flags[rej.flag], hashes[rej.flag] = old, prev.hashes[rej.flag]
				report.Kept = true
			}
		}

		rejected = append(rejected, report)
	}

	return tables{flags: flags, hashes: hashes, rejected: rejected}
}
