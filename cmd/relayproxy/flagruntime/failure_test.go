package flagruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/flagruntime"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// An object whose bytes do not decode reports the decoder error; a stale or missing state reports its own reason;
// when the same undecodable bytes come back as ok the decoder error is reported again, without parsing them again.
func TestUndecodableObjectReportsTheDecoderErrorAgain(t *testing.T) {
	t.Parallel()

	const staleReason = "etag unchanged"

	tests := []struct {
		name                string
		kind                string
		bad                 []byte
		good                func(t *testing.T) []byte
		objects             func(t *testing.T, object flagruntime.Object) objs
		wantDecoderReason   string // a part of the error that the decoder reports for the bad bytes
		wantStatesInOrder   []flagruntime.ObjectState
		wantReasonsContains []string
	}{
		{
			name: "flags object", kind: flagruntime.KindFlags, bad: []byte("x: [unterminated"),
			good: func(*testing.T) []byte { return flagsYAML(1, "x") },
			objects: func(t *testing.T, object flagruntime.Object) objs {
				t.Helper()

				return objs{"f1": object, setsName: setsObj(t, fset("a", src("f1"), keyA))}
			},
			wantDecoderReason: "decoding the flags",
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
			wantDecoderReason: "controlplane: decode",
		},
	}

	for idx := range tests {
		tests[idx].wantStatesInOrder = []flagruntime.ObjectState{
			flagruntime.ObjectOK, flagruntime.ObjectInvalid, flagruntime.ObjectStale,
			flagruntime.ObjectInvalid, flagruntime.ObjectOK,
		}
		tests[idx].wantReasonsContains = []string{
			"",
			tests[idx].wantDecoderReason,
			staleReason,
			tests[idx].wantDecoderReason,
			"",
		}
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
			withBytes := func(data []byte) flagruntime.Object {
				return flagruntime.Object{
					Kind:     tt.kind,
					Format:   controlplane.FormatYAML,
					Bytes:    data,
					Checksum: controlplane.ChecksumOf(data).Value,
				}
			}
			stale := flagruntime.Object{Kind: tt.kind, State: flagruntime.ObjectStale, Reason: staleReason}
			sequence := []flagruntime.Object{
				withBytes(tt.good(t)), withBytes(tt.bad), stale, withBytes(tt.bad), withBytes(tt.good(t)),
			}

			parsesAfterTheBadBytes := 0

			for step, object := range sequence {
				status := buildAndCommitStatus(t, builder, tt.objects(t, object))[objectName(tt.kind)]

				assert.Equal(t, tt.wantStatesInOrder[step], status.State, "state at step %d", step)
				assert.Contains(t, status.Reason, tt.wantReasonsContains[step], "reason at step %d", step)

				if step == 1 {
					parsesAfterTheBadBytes = parses
				}
			}

			assert.Equal(
				t,
				parsesAfterTheBadBytes,
				parses-1,
				"only the fixed bytes were parsed after the first failure",
			)
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
