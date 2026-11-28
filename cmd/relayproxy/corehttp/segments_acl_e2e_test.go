//go:build docker

package corehttp_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
)

// The flags of the ACL test: one for each feature of the stores that needs its own commands.
const (
	kindValkey    = "valkey"
	kindRedis     = "redis"
	cmdExists     = "+exists"
	cmdGet        = "+get"
	cmdSmismember = "+smismember"
	cmdSscan      = "+sscan"
	flagUnknown   = "f-unknown"
	flagSnap      = "f-snap"
	flagSet       = "f-set"
	flagKeys      = "f-keys"
)

const aclFlagsYAML = `
f-set:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("aset")', variation: on}
  defaultRule: {name: default, variation: off}
f-keys:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("akeys")', variation: on}
  defaultRule: {name: default, variation: off}
f-unknown:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("aunknown")', variation: on}
  defaultRule: {name: default, variation: off}
f-snap:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("asnap")', variation: on}
  defaultRule: {name: default, variation: off}
`

// aclSegmentsYAML has one segment for each feature: a set that is asked (lookup), keys that are asked, a set whose
// missing key is unknown (the existence check, a transaction for valkey), and a set that is pulled (snapshot).
const aclSegmentsYAML = segHeaderYAML +
	"  - name: aset\n    connection: acl\n    mode: lookup\n    key: aud\n" +
	"  - name: akeys\n    connection: acl\n    mode: lookup\n    layout: keys\n    key: \"staff:\"\n" +
	"  - name: aunknown\n    connection: acl\n    mode: lookup\n    key: aud\n    missingKey: unknown\n" +
	"  - name: asnap\n    connection: acl\n    key: snap\n    version: {key: snap-version, interval: 100ms}\n"

// aclCase is a kind of store, the commands of its documented read-only ACL line, and what each of them is needed for.
type aclCase struct {
	// needed maps a command of the line to the flags that stop working without it.
	needed map[string][]string
	kind   string
	// line is the documented ACL line: the commands, in the form of ACL SETUSER.
	line []string
}

func aclCases() []aclCase {
	return []aclCase{
		{
			kind: kindValkey,
			line: []string{"+hello", cmdSmismember, "+exists", cmdSscan, cmdGet, "+multi", "+exec"},
			needed: map[string][]string{
				// not needed: without HELLO the client opens the connection with AUTH (RESP2) and every feature works
				"+hello":      {},
				cmdSmismember: {flagSet, flagUnknown},
				cmdExists:     {flagKeys, flagUnknown},
				cmdSscan:      {flagSnap},
				cmdGet:        {flagSnap},
				"+multi":      {flagUnknown},
				"+exec":       {flagUnknown},
			},
		},
		{
			kind: kindRedis,
			line: []string{cmdSmismember, "+exists", cmdSscan, cmdGet},
			needed: map[string][]string{
				cmdSmismember: {flagSet, flagUnknown},
				cmdExists:     {flagKeys, flagUnknown},
				cmdSscan:      {flagSnap},
				cmdGet:        {flagSnap},
			},
		},
	}
}

func aclRig(t *testing.T, store *valkeyStore, kind, user, password string) *v2 {
	t.Helper()

	file := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(file, []byte(password+"\n"), 0o600))

	conn := "  acl: {type: " + kind + ", addresses: [\"" + store.address + "\"], username: " + user +
		", password: {fromFile: " + file + "}, lookupTimeout: 300ms, timeouts: {connect: 500ms}"
	if kind == kindValkey {
		conn += ", forceSingleClient: true"
	}

	conn += "}\n"

	rig := newV2With(
		t,
		&v2Options{
			connections: conn,
			extra:       resolverSection,
			cold:        true,
			secrets:     bootstrap.LoadOptions{ReadFile: os.ReadFile},
		},
	)
	rig.flags[dynFlagsObject] = []byte(aclFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(aclSegmentsYAML)}
	rig.sets = []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject, dynFlagsObject}, SegmentSources: []string{segObjectName},
		APIKeys: []string{tenantKey},
	}}

	rig.publish(t)
	rig.poll(t)

	return rig
}

// awaitFlag waits until the member of a flag is a member (healthy) or answers STORE_ERROR.
func awaitFlag(t *testing.T, rig *v2, flag string, wantMember bool, msg string) {
	t.Helper()

	// A member is certain whatever the existence check says (a SMISMEMBER hit is a member), so a store that refuses
	// EXISTS still answers for alice: the denied case asks about a value that is in no set, which needs every command.
	who := "alice"
	if !wantMember {
		who = "mallory"
	}

	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		got, err := tryEval(rig, flag, who)
		if !assert.NoError(collect, err, msg) {
			return
		}

		if wantMember {
			assert.True(collect, isMember(got), "%s: %+v", msg, got)
		} else {
			assert.True(collect, isStoreError(got), "%s: %+v", msg, got)
		}
	}, eventually, poll, msg)
}

// The documented read-only ACL line is enough and minimal: with it the relay serves every feature of the stores (the
// set and the keys layout, the existence check, the snapshot), and without any one command the feature that needs it
// answers STORE_ERROR while the relay stays ready. The user cannot write.
func TestE2E_ReadOnlyACLIsSufficientAndMinimal(t *testing.T) {
	t.Parallel()

	allFlags := []string{flagSet, flagKeys, flagUnknown, flagSnap}

	for _, acl := range aclCases() {
		t.Run(acl.kind, func(t *testing.T) {
			t.Parallel()

			store := startValkey(t)
			store.add(t, "aud", "alice")
			store.add(t, "snap", "alice")

			for _, key := range []string{"snap-version", "staff:alice"} {
				require.NoError(
					t,
					store.client.Do(t.Context(), store.client.B().Set().Key(key).Value("1").Build()).Error(),
				)
			}

			// a failure names the commands that the store refused
			t.Cleanup(func() {
				if !t.Failed() {
					return
				}

				entries, err := store.client.Do(context.Background(), store.client.B().Arbitrary("ACL", "LOG").Build()).
					ToArray()
				if err != nil {
					t.Logf("ACL LOG: %v", err)

					return
				}

				t.Logf("ACL LOG has %d entries", len(entries))

				for i := range entries {
					entry := entries[i]
					fields, _ := entry.AsMap()
					object, user := fields["object"], fields["username"]
					objectText, _ := object.ToString()
					userText, _ := user.ToString()
					t.Logf("denied for %s: %s", userText, objectText)
				}
			})

			grant := func(user string, commands []string) {
				args := append([]string{user, "on", ">pw", "~*", "-@all"}, commands...)
				cmd := store.client.B().Arbitrary("ACL", "SETUSER").Args(args...).Build()
				require.NoError(t, store.client.Do(t.Context(), cmd).Error())
			}

			t.Run("the line is on the page", func(t *testing.T) {
				t.Parallel()

				raw, err := os.ReadFile("../../../docs/configure-flags/segments-dynamic.md")
				require.NoError(t, err)

				assert.Contains(
					t,
					string(raw),
					"ACL SETUSER relay on ><password> ~* -@all "+strings.Join(acl.line, " "),
				)
			})

			t.Run("the line is sufficient", func(t *testing.T) {
				t.Parallel()

				grant("relay", acl.line)

				rig := aclRig(t, store, acl.kind, "relay", "pw")

				for _, flag := range allFlags {
					awaitFlag(t, rig, flag, true, flag+" works with the documented line")
				}

				rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)

				// the user cannot write, and can read what the line says
				for cmd, wantOK := range map[string][]string{
					"SET aud x": nil, "SADD aud x": nil, "DEL aud": nil, "FLUSHALL": nil, "CONFIG GET *": nil,
					"SMISMEMBER aud alice": {"OK"}, "GET snap-version": {"OK"},
				} {
					fields := strings.Fields(cmd)
					res, err := store.client.Do(
						t.Context(),
						store.client.B().
							Arbitrary("ACL", "DRYRUN").
							Args("relay", fields[0]).
							Args(fields[1:]...).
							Build(),
					).ToString()
					require.NoError(t, err, cmd)

					if wantOK != nil {
						assert.Equal(t, "OK", res, cmd)
					} else {
						assert.Contains(t, res, "no permissions", cmd)
					}
				}
			})

			for _, removed := range acl.line {
				t.Run("without "+removed, func(t *testing.T) {
					t.Parallel()

					user := "no-" + strings.TrimPrefix(removed, "+")
					grant(user, slices.DeleteFunc(slices.Clone(acl.line), func(c string) bool { return c == removed }))

					rig := aclRig(t, store, acl.kind, user, "pw")

					for _, flag := range acl.needed[removed] {
						awaitFlag(t, rig, flag, false, flag+" needs "+removed)
					}

					if len(acl.needed[removed]) == 0 {
						for _, flag := range allFlags {
							awaitFlag(t, rig, flag, true, flag+" works without "+removed)
						}
					}

					rig.wantProbe(t, "/readyz", http.StatusOK, bodyServing)
				})
			}
		})
	}
}
