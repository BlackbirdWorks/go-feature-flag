package runtime //nolint:revive,nolintlint // the name is fixed: the package is cmd/relayproxy/runtime

import (
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

	out := &parsedFlags{valid: make(map[string]*flagdef.InternalFlag, len(raw))}

	for _, name := range names {
		def := raw[name]
		converted := def.Convert()

		verr := converted.IsValid(svc)
		if verr != nil {
			out.rejected = append(out.rejected, rejection{flag: name, reason: verr.Error()})

			continue
		}

		out.valid[name] = &converted
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

// buildTable merges a parse result with the previous table of the same object (last-known-good per flag):
// a flag that failed validation keeps its previous definition, and a flag whose definition did not change
// keeps its previous pointer, so unchanged flags exist once across versions.
func buildTable(
	parsed *parsedFlags,
	prev *objState,
) (map[string]*flagdef.InternalFlag, []FlagRejection, bool) {
	rejected := make([]FlagRejection, 0, len(parsed.rejected))

	hasPrev := prev != nil && prev.loaded
	if !hasPrev && len(parsed.rejected) == 0 {
		return parsed.valid, rejected, false
	}

	flags := make(map[string]*flagdef.InternalFlag, len(parsed.valid)+len(parsed.rejected))

	for key, fresh := range parsed.valid {
		if hasPrev {
			if old, ok := prev.flags[key]; ok && reflect.DeepEqual(old, fresh) {
				flags[key] = old

				continue
			}
		}

		flags[key] = fresh
	}

	kept := false

	for _, rej := range parsed.rejected {
		report := FlagRejection{Flag: rej.flag, Reason: rej.reason}

		if hasPrev {
			if old, ok := prev.flags[rej.flag]; ok {
				flags[rej.flag] = old
				report.Kept = true
				kept = true
			}
		}

		rejected = append(rejected, report)
	}

	return flags, rejected, kept
}
