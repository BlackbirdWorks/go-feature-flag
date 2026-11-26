package flagruntime_test

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

// flagsYAML writes a flags file: every named flag has one variation "v" with the value, and serves it.
func flagsYAML(value int, names ...string) []byte {
	var out strings.Builder

	for _, name := range names {
		fmt.Fprintf(&out, "%s:\n  variations:\n    v: %d\n  defaultRule:\n    variation: v\n", name, value)
	}

	return []byte(out.String())
}

// badYAML writes a flag named y without variations, which the engine rejects.
func badYAML() []byte {
	return []byte("y:\n  defaultRule:\n    variation: v\n")
}

func join(parts ...[]byte) []byte {
	return slices.Concat(parts...)
}

func obj(kind string, data []byte) flagruntime.Object {
	return flagruntime.Object{
		Kind: kind, Format: controlplane.FormatYAML, Checksum: controlplane.ChecksumOf(data).Value, Bytes: data,
	}
}

// flagsObj is a flags object whose flags all carry value.
func flagsObj(value int, names ...string) flagruntime.Object {
	return obj(flagruntime.KindFlags, flagsYAML(value, names...))
}

func setsObj(tb testing.TB, sets ...controlplane.FlagSet) flagruntime.Object {
	tb.Helper()

	data, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{FlagSets: sets})
	require.NoError(tb, err)

	return obj(flagruntime.KindFlagSets, data)
}

func fset(name string, sources []string, keys ...string) controlplane.FlagSet {
	return controlplane.FlagSet{Name: name, Sources: sources, APIKeys: keys}
}

func src(names ...string) []string { return names }

// Names the tests repeat.
const (
	setsName   = "sets"
	keyA       = "key-a"
	keyB       = "key-b"
	pathAX     = "a/x"
	pathAY     = "a/y"
	pathBX     = "b/x"
	adminOne   = "admin-1"
	docs1      = "docs-1"
	docs2      = "docs-2"
	sharedName = "shared"
	extraName  = "extra"
	endSet     = "zz-end"
)

type objs map[string]flagruntime.Object

func content(gen uint64, in objs) *flagruntime.Content {
	return &flagruntime.Content{Generation: gen, Objects: in}
}

// with returns a copy of in with the given objects added or replaced.
func (in objs) with(more objs) objs {
	out := maps.Clone(in)
	maps.Copy(out, more)

	return out
}

func (in objs) without(names ...string) objs {
	out := maps.Clone(in)

	for _, name := range names {
		delete(out, name)
	}

	return out
}

func newService(tb testing.TB) *flagdef.Service {
	tb.Helper()

	svc := flagdef.NewService()
	tb.Cleanup(svc.Close)

	return svc
}

// value returns the value of the variation "v" of a flag, or nil.
func value(set *flagruntime.FlagSet, name string) any {
	fl, ok := set.Flag(name)
	if !ok {
		return nil
	}

	return fl.GetVariationValue("v")
}

func flagNames(set *flagruntime.FlagSet) []string {
	if set == nil {
		return nil
	}

	out := make([]string, 0, len(set.Flags))
	for name := range set.Flags {
		out = append(out, name)
	}

	return sortedStrings(out)
}

func sortedStrings(in []string) []string {
	return slices.Sorted(slices.Values(in))
}
