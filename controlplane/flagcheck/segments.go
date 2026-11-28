package flagcheck

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

// ErrSegments is matched (errors.Is) by the errors of the segment checks of this package.
var ErrSegments = errors.New("flagcheck: invalid segments")

// Definition converts a segment of a segments document to the definition of the engine. A segment with a
// connection is a dynamic segment: its store kind is not known to a publisher, so the definition has the type of a
// Redis store, which the engine validates like the other store types. A static segment is of the type file. The
// second result is false when a field cannot be converted (a duration that does not parse): the document validation
// reports it.
func Definition(seg *controlplane.SegmentDefinition) (segment.Definition, bool) {
	def := segment.Definition{
		Name:       seg.Name,
		Type:       segment.TypeFile,
		OnError:    segment.OnError(seg.OnError),
		Mode:       segment.Mode(seg.Mode),
		MissingKey: segment.MissingKey(seg.MissingKey),
		Layout:     segment.Layout(seg.Layout),
	}

	if seg.Connection != "" {
		def.Type = segment.TypeRedis
	}

	if seg.Version == nil {
		return def, true
	}

	spec := &segment.VersionSpec{Value: seg.Version.Value, Key: seg.Version.Key}

	if seg.Version.Interval != "" {
		interval, err := time.ParseDuration(seg.Version.Interval)
		if err != nil {
			return def, false
		}

		spec.Interval = interval
	}

	def.Version = spec

	return def, true
}

// SegmentProblem is one segment that the engine rejects.
type SegmentProblem struct {
	// Err is the reason given by the engine.
	Err error
	// Segment is the name of the segment.
	Segment string
}

// CheckSegments decodes a segments document, validates it with the rules of the controlplane package, and then
// validates every segment with the validation of the engine, so that a segment that passes is not dropped when the
// relay builds its catalog. It returns nil, or an error that matches ErrSegments.
func CheckSegments(body []byte) error {
	doc, _, err := controlplane.UnmarshalSegments(body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSegments, err)
	}

	err = controlplane.ValidateSegments(doc)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSegments, err)
	}

	var problems []string

	for i := range doc.Segments {
		def, ok := Definition(&doc.Segments[i])
		if !ok {
			continue
		}

		verr := def.Validate()
		if verr != nil {
			problems = append(problems, "segments["+strconv.Itoa(i)+"]: "+verr.Error())
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: the engine rejects: %s", ErrSegments, strings.Join(problems, "; "))
	}

	return nil
}

// FlagFile is one flag object of a flag set.
type FlagFile struct {
	// Name is the name of the manifest object.
	Name string
	// Format is "yaml" or "json".
	Format string
	// Body is the flag file.
	Body []byte
}

// SegmentFile is one segments object of a flag set.
type SegmentFile struct {
	// Doc is the decoded document.
	Doc *controlplane.SegmentsDocument
	// Name is the name of the manifest object.
	Name string
}

// ReferenceProblem is one reference to a segment that cannot be resolved, or one name that is defined twice.
type ReferenceProblem struct {
	// Object is the flag object, or the segments object for a duplicate name.
	Object string
	// Flag is the key of the flag that uses the segment (empty for a duplicate name).
	Flag string
	// Segment is the name of the segment.
	Segment string
	// Reason says what is wrong.
	Reason string
	// Rule is the position of the targeting rule in the flag, or -1.
	Rule int
}

// ReferenceError lists the problems of CheckSegmentReferences.
type ReferenceError struct {
	// FlagSet is the name of the flag set.
	FlagSet string
	// Problems are the problems, in order.
	Problems []ReferenceProblem
}

// Error implements error.
func (e *ReferenceError) Error() string {
	parts := make([]string, 0, len(e.Problems))

	for i := range e.Problems {
		prob := &e.Problems[i]
		if prob.Flag == "" {
			parts = append(parts, fmt.Sprintf("flag set %s: segment %s: %s", e.FlagSet, prob.Segment, prob.Reason))

			continue
		}

		parts = append(parts, fmt.Sprintf("flag set %s: flag %s, rule %d: segment %s: %s",
			e.FlagSet, safeKey(prob.Flag), prob.Rule, prob.Segment, prob.Reason))
	}

	return strings.Join(parts, "; ")
}

// Is reports a match with ErrSegments.
func (*ReferenceError) Is(target error) bool { return target == ErrSegments }

// CheckSegmentReferences checks the segments of one flag set: a segment name must be defined once in all its
// segment sources, and every segment("name") that a targeting rule of a flag uses must be defined there. A flag
// set that has no segment source and no rule that uses a segment is fine. The queries are read with the engine's own
// reader of the segment terms of a rule. It returns nil or a *ReferenceError.
func CheckSegmentReferences(flagSet string, flags []FlagFile, sources []SegmentFile) error {
	out := &ReferenceError{FlagSet: flagSet}
	defined := map[string]string{}

	for i := range sources {
		for j := range sources[i].Doc.Segments {
			name := sources[i].Doc.Segments[j].Name
			if first, dup := defined[name]; dup {
				out.Problems = append(out.Problems, ReferenceProblem{
					Object: sources[i].Name, Segment: name, Rule: -1,
					Reason: "defined twice in the segment sources of the flag set, first in " + first,
				})

				continue
			}

			defined[name] = sources[i].Name
		}
	}

	svc := flagdef.NewService()
	defer svc.Close()

	for i := range flags {
		out.Problems = append(out.Problems, unresolved(svc, &flags[i], defined)...)
	}

	if len(out.Problems) == 0 {
		return nil
	}

	return out
}

// unresolved returns the references of the flags of one file that are not defined.
func unresolved(svc *flagdef.Service, file *FlagFile, defined map[string]string) []ReferenceProblem {
	dtos, err := cache.ConvertToFlagStruct(file.Body, file.Format)
	if err != nil {
		return nil // the flag object itself is checked by Check
	}

	var problems []ReferenceProblem

	for _, key := range slices.Sorted(maps.Keys(dtos)) {
		flagDto := dtos[key]
		internal := flagDto.Convert()
		rules := internal.GetRules()

		reported := map[string]bool{}

		for index := range rules {
			one := internal
			one.Rules = &[]flagdef.Rule{rules[index]}
			one.Scheduled = nil

			for _, name := range one.SegmentNames(svc) {
				if _, ok := defined[name]; !ok {
					reported[name] = true
					problems = append(problems, ReferenceProblem{
						Object: file.Name, Flag: key, Rule: index, Segment: name,
						Reason: "is not defined in the segment sources of the flag set",
					})
				}
			}
		}

		// the rules that a scheduled step adds
		for _, name := range internal.SegmentNames(svc) {
			if _, ok := defined[name]; !ok && !reported[name] {
				problems = append(problems, ReferenceProblem{
					Object: file.Name, Flag: key, Rule: -1, Segment: name,
					Reason: "is not defined in the segment sources of the flag set (used by a scheduled step)",
				})
			}
		}
	}

	return problems
}
