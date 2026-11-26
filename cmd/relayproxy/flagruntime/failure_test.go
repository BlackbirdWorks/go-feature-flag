package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// An object whose bytes do not decode reports the decoder error; a stale or missing state reports its own reason;
// when the same undecodable bytes come back as ok the decoder error is reported again, without parsing them again.
func TestUndecodableObjectReportsTheDecoderErrorAgain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		good    func(t *testing.T) []byte
		objects func(t *testing.T, object flagruntime.Object) objs
		name    string
		kind    string
		bad     []byte
	}{
		{
			name: "flags object", kind: flagruntime.KindFlags, bad: []byte("x: [unterminated"),
			good: func(*testing.T) []byte { return flagsYAML(1, "x") },
			objects: func(t *testing.T, object flagruntime.Object) objs {
				t.Helper()

				return objs{"f1": object, setsName: setsObj(t, fset("a", src("f1"), keyA))}
			},
		},
		{
			name: "flag-set document", kind: flagruntime.KindFlagSets, bad: []byte("flagSets: [unterminated"),
			good: func(t *testing.T) []byte {
				t.Helper()

				return setsObj(t, fset("a", src("f1"), keyA)).Bytes
			},
			objects: func(t *testing.T, object flagruntime.Object) objs {
				t.Helper()

				return objs{"f1": flagsObj(1, "x"), setsName: object}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parses := 0
			builder := flagruntime.NewBuilder(newService(t), flagruntime.BuilderOptions{
				OnParse: func(event flagruntime.ParseEvent) {
					if event.Kind == tt.kind {
						parses++
					}
				},
			})
			target := func(object flagruntime.Object) *flagruntime.ObjectStatus {
				return buildAndCommitStatus(t, builder, tt.objects(t, object))[objectName(tt.kind)]
			}
			withBytes := func(data []byte) flagruntime.Object {
				return flagruntime.Object{
					Kind:     tt.kind,
					Format:   controlplane.FormatYAML,
					Bytes:    data,
					Checksum: controlplane.ChecksumOf(data).Value,
				}
			}

			good := target(withBytes(tt.good(t)))
			require.Equal(t, flagruntime.ObjectOK, good.State)

			invalid := target(withBytes(tt.bad))
			require.Equal(t, flagruntime.ObjectInvalid, invalid.State)
			require.NotEmpty(t, invalid.Reason)

			decoderError := invalid.Reason
			parsesOfTheBadBytes := parses

			stale := target(flagruntime.Object{Kind: tt.kind, State: flagruntime.ObjectStale, Reason: "etag unchanged"})
			assert.Equal(t, flagruntime.ObjectStale, stale.State)
			assert.Equal(t, "etag unchanged", stale.Reason)

			again := target(withBytes(tt.bad))
			assert.Equal(t, flagruntime.ObjectInvalid, again.State)
			assert.Equal(t, decoderError, again.Reason, "the reason is the decoder error again, not the stale one")
			assert.Equal(t, parsesOfTheBadBytes, parses, "the failure of the same bytes is not parsed again")

			fixed := target(withBytes(tt.good(t)))
			assert.Equal(t, flagruntime.ObjectOK, fixed.State)
			assert.Empty(t, fixed.Reason)
		})
	}
}

func objectName(kind string) string {
	if kind == flagruntime.KindFlags {
		return "f1"
	}

	return setsName
}

// buildAndCommitStatus builds a content (the builder does not look at generations) and returns the status of
// the objects.
func buildAndCommitStatus(t *testing.T, builder *flagruntime.Builder, in objs) map[string]*flagruntime.ObjectStatus {
	t.Helper()

	return buildAndCommit(t, builder, content(1, in)).Runtime.Status.Objects
}
