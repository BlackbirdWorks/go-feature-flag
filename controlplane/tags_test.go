package controlplane_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// TestStructTagSnapshot pins every exported field and its tags: a change of the wire format fails here.
// Update testdata/golden/tags.txt (UPDATE_GOLDEN=1) only for an additive change of a new version.
func TestStructTagSnapshot(t *testing.T) {
	t.Parallel()

	types := []reflect.Type{
		reflect.TypeFor[controlplane.Manifest](),
		reflect.TypeFor[controlplane.ManifestObject](),
		reflect.TypeFor[controlplane.Checksum](),
		reflect.TypeFor[controlplane.FlagSetDocument](),
		reflect.TypeFor[controlplane.FlagSet](),
		reflect.TypeFor[controlplane.Flag](),
		reflect.TypeFor[controlplane.Rule](),
		reflect.TypeFor[controlplane.ProgressiveRollout](),
		reflect.TypeFor[controlplane.ProgressiveRolloutStep](),
		reflect.TypeFor[controlplane.ScheduledStep](),
		reflect.TypeFor[controlplane.Experimentation](),
	}

	var out strings.Builder

	for _, typ := range types {
		for field := range typ.Fields() {
			fmt.Fprintf(&out, "%s.%s %s json=%q yaml=%q\n", typ.Name(), field.Name, field.Type,
				field.Tag.Get("json"), field.Tag.Get("yaml"))
		}
	}

	checkGolden(t, "tags.txt", []byte(out.String()))
}
