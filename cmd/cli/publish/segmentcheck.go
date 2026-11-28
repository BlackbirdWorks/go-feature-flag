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

	var problems []error

	for i := range objects {
		if objects[i].Kind != controlplane.KindFlagSets {
			continue
		}

		doc, _, err := controlplane.UnmarshalFlagSet(objects[i].Body)
		if err != nil {
			continue // the decoding is reported by the validation of the object
		}

		for j := range doc.FlagSets {
			err = checkFlagSetReferences(&doc.FlagSets[j], byName)
			if err != nil {
				problems = append(problems, err)
			}
		}
	}

	return errors.Join(problems...)
}

func checkFlagSetReferences(set *controlplane.FlagSet, byName map[string]*s3publish.Object) error {
	var (
		flags    []flagcheck.FlagFile
		segments []flagcheck.SegmentFile
	)

	for _, name := range set.Sources {
		obj, ok := byName[name]
		if !ok || obj.Kind != controlplane.KindFlags {
			continue
		}

		flags = append(flags, flagcheck.FlagFile{Name: name, Format: obj.Format, Body: obj.Body})
	}

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
