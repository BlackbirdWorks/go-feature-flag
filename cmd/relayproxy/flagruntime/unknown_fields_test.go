package flagruntime_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

func TestBuilder_ObjectUnknownFields(t *testing.T) {
	t.Parallel()

	svc := newService(t)

	twelveExtras := func() []byte {
		var buf strings.Builder

		buf.WriteString("f:\n  variations: {v: 1}\n  defaultRule: {variation: v}\n")

		for i := range 12 {
			fmt.Fprintf(&buf, "  future%02d: true\n", i)
		}

		return []byte(buf.String())
	}()

	tests := []struct {
		build      func(t *testing.T) objs
		wantValue  any
		name       string
		objectName string
		wantKind   string
		wantPaths  []string
		wantCount  int
	}{
		{
			name:       "flags object reports unknown fields while serving",
			objectName: "a",
			wantKind:   flagruntime.KindFlags,
			wantCount:  2,
			wantPaths:  []string{"f.defaultRule.extraRule", "f.extraFlag"},
			wantValue:  int(1),
			build: func(t *testing.T) objs {
				t.Helper()

				raw := []byte(
					"f:\n  extraFlag: secret\n  defaultRule:\n    extraRule: 1\n    variation: v\n" +
						"  variations: {v: 1}\n",
				)

				return objs{
					"a":      obj(flagruntime.KindFlags, raw),
					setsName: setsObj(t, fset("s", src("a"), keyA)),
				}
			},
		},
		{
			name:       "flag paths are bounded at ten",
			objectName: "a",
			wantKind:   flagruntime.KindFlags,
			wantCount:  12,
			wantValue:  int(1),
			wantPaths: []string{
				"f.future00", "f.future01", "f.future02", "f.future03", "f.future04",
				"f.future05", "f.future06", "f.future07", "f.future08", "f.future09",
			},
			build: func(t *testing.T) objs {
				t.Helper()

				return objs{
					"a":      obj(flagruntime.KindFlags, twelveExtras),
					setsName: setsObj(t, fset("s", src("a"), keyA)),
				}
			},
		},
		{
			name:       "toml flags object leaves unknown fields zero",
			objectName: "a",
			wantKind:   flagruntime.KindFlags,
			wantCount:  0,
			wantValue:  int64(1),
			wantPaths:  nil,
			build: func(t *testing.T) objs {
				t.Helper()

				toml := []byte("[f]\n[f.variations]\nv = 1\n[f.defaultRule]\nvariation = \"v\"\n")

				return objs{
					"a": {
						Kind: flagruntime.KindFlags, Format: controlplane.FormatTOML,
						Checksum: controlplane.ChecksumOf(toml).Value, Bytes: toml,
					},
					setsName: setsObj(t, fset("s", src("a"), keyA)),
				}
			},
		},
		{
			name:       "flagSets document reports unknown fields",
			objectName: setsName,
			wantKind:   flagruntime.KindFlagSets,
			wantCount:  1,
			wantValue:  int(1),
			wantPaths:  []string{"extraTop"},
			build: func(t *testing.T) objs {
				t.Helper()

				doc := []byte("apiVersion: semaphore/v1\nkind: FlagSet\nextraTop: 1\n" +
					"flagSets:\n  - name: s\n    sources: [a]\n    apiKeys: [key-a]\n")

				return objs{
					"a":      flagsObj(1, "f"),
					setsName: obj(flagruntime.KindFlagSets, doc),
				}
			},
		},
		{
			name:       "segments document reports unknown fields",
			objectName: "segs",
			wantKind:   flagruntime.KindSegments,
			wantCount:  1,
			wantValue:  int(1),
			wantPaths:  []string{"extraSeg"},
			build: func(t *testing.T) objs {
				t.Helper()

				seg := []byte("apiVersion: semaphore/v1\nkind: Segments\nextraSeg: true\n" +
					"segments:\n  - name: beta\n    members: [u1]\n")

				return objs{
					"a":      flagsObj(1, "f"),
					"segs":   obj(flagruntime.KindSegments, seg),
					setsName: setsObj(t, fset("s", src("a"), keyA)),
				}
			},
		},
	}

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builder := flagruntime.NewBuilder(svc, flagruntime.BuilderOptions{})
			built, err := builder.Build(t.Context(), content(1, tt.build(t)))
			require.NoError(t, err)
			builder.Commit(built)

			status := built.Runtime.Status.Objects[tt.objectName]
			require.NotNil(t, status)
			assert.Equal(t, tt.wantKind, status.Kind)
			assert.Equal(t, tt.wantCount, status.UnknownFields)
			assert.Equal(t, tt.wantPaths, status.UnknownFieldPaths)
			assert.Equal(t, tt.wantValue, value(built.Runtime.ByKey[keyA], "f"))
		})
	}
}
