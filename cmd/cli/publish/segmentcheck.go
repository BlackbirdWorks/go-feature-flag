package publish

import (
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

// checkSegments validates a segments document with the engine of the relay (the flagcheck package).
func checkSegments(obj *s3publish.Object) error {
	err := flagcheck.CheckSegments(obj.Body)
	if err != nil {
		return fmt.Errorf("engine check: %w", err)
	}

	return nil
}

// checkReferences checks, for every flag set, that the segments that the flags of its sources use are defined in its
// segment sources, and that a name is defined once there. A flag set that does not resolve its sources is left to the
// validation of the documents.
func checkReferences(objects []s3publish.Object) error {
	byName := make(map[string]*s3publish.Object, len(objects))
	for i := range objects {
		byName[objects[i].Name] = &objects[i]
	}

	var (
		problems []error
		keyless  []flagcheck.KeylessSet
	)

	for i := range objects {
		if objects[i].Kind != controlplane.KindFlagSets {
			continue
		}

		doc, _, err := controlplane.UnmarshalFlagSet(objects[i].Body)
		if err != nil {
			continue // the decoding is reported by the validation of the object
		}

		for idx := range doc.FlagSets {
			err = checkFlagSetReferences(&doc.FlagSets[idx], byName)
			if err != nil {
				problems = append(problems, err)
			}

			if len(doc.FlagSets[idx].APIKeys) == 0 {
				keyless = append(keyless, flagcheck.KeylessSet{
					Name: doc.FlagSets[idx].Name, Flags: flagFiles(&doc.FlagSets[idx], byName),
				})
			}
		}
	}

	// the flag sets without keys are public, and the relay rejects a generation in which two of them define a flag key
	// one error for each flag key, so that a verify report lists every one of them
	if public, ok := errors.AsType[*flagcheck.PublicError](flagcheck.CheckPublicDuplicates(keyless)); ok {
		for i := range public.Problems {
			one := &flagcheck.PublicError{Problems: public.Problems[i : i+1]}
			problems = append(problems, fmt.Errorf("engine check: %w", one))
		}
	}

	return errors.Join(problems...)
}

// flagFiles are the flag objects of the sources of a flag set that are part of the publication, in order.
func flagFiles(set *controlplane.FlagSet, byName map[string]*s3publish.Object) []flagcheck.FlagFile {
	var flags []flagcheck.FlagFile

	for _, name := range set.Sources {
		obj, ok := byName[name]
		if !ok || obj.Kind != controlplane.KindFlags {
			continue
		}

		flags = append(flags, flagcheck.FlagFile{Name: name, Format: obj.Format, Body: obj.Body})
	}

	return flags
}

func checkFlagSetReferences(set *controlplane.FlagSet, byName map[string]*s3publish.Object) error {
	flags := flagFiles(set, byName)

	var segments []flagcheck.SegmentFile

	for _, name := range set.SegmentSources {
		obj, ok := byName[name]
		if !ok || obj.Kind != controlplane.KindSegments {
			// not read (another connection, skipped by verify): its names are unknown
			segments = append(segments, flagcheck.SegmentFile{Name: name})

			continue
		}

		doc, _, err := controlplane.UnmarshalSegments(obj.Body)
		if err != nil {
			segments = append(segments, flagcheck.SegmentFile{Name: name})

			continue
		}

		segments = append(segments, flagcheck.SegmentFile{Name: name, Doc: doc})
	}

	err := flagcheck.CheckSegmentReferences(set.Name, flags, segments)
	if err != nil {
		return fmt.Errorf("engine check: %w", err)
	}

	return nil
}
