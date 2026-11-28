//go:build docker

package corehttp_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
)

const (
	canaryMember  = "CANARY-member-id-8c1f"
	canaryOther   = "CANARY-other-id-5e2a"
	canarySnap    = "CANARY-snap-id-71b9"
	canaryOutage  = "CANARY-outage-id-d04c"
	canaryTyped   = "CANARY-typed-id-2f68"
	canaryBadBody = "CANARY-bad-body-9a13"
	canaryPass    = "CANARY-store-password-b5e7"

	canaryFlagsYAML = `
f-lookup:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("aset")', variation: on}
  defaultRule: {name: default, variation: off}
f-snap:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("asnap")', variation: on}
  defaultRule: {name: default, variation: off}
f-typed:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'user.id in segment("aset")', variation: on}
  defaultRule: {name: default, variation: off}
f-rejected:
  variations: {on: true, off: false}
  targeting:
    - {name: m, query: 'targetingKey in segment("arejected")', variation: on}
  defaultRule: {name: default, variation: off}
`
	canarySegmentsYAML = segHeaderYAML +
		"  - name: aset\n    connection: acl\n    mode: lookup\n    key: aud\n" +
		"  - name: asnap\n    connection: acl\n    key: snap\n" +
		"    version: {key: snap-version, interval: 100ms}\n" +
		"  - name: arejected\n    connection: nowhere\n    mode: lookup\n"
)

// A recognisable member id is evaluated through every path of the dynamic segments (a lookup that hits, one that
// misses, a snapshot, a definition that is rejected, a typed attribute, a store that is gone, a request that is
// refused), at debug level, and the password of the store is read from a file and used: neither the id nor the
// password is in any log line, in /info, in the metrics, in the flag configuration or in any error body.
func TestE2E_NoMemberIDAndNoPasswordInAnyOutput(t *testing.T) {
	t.Parallel()

	store := startValkey(t)
	require.NoError(t, store.client.Do(t.Context(), store.client.B().Arbitrary("ACL", "SETUSER").
		Args("relay", "on", ">"+canaryPass, "~*", "+@all").Build()).Error())

	for _, member := range []string{canaryMember, canarySnap, canaryTyped} {
		store.add(t, "aud", member)
	}

	store.add(t, "snap", canarySnap)
	require.NoError(
		t,
		store.client.Do(t.Context(), store.client.B().Set().Key("snap-version").Value("1").Build()).Error(),
	)

	proxy := newOutage(t, store.address)
	file := filepath.Join(t.TempDir(), "password")
	require.NoError(t, os.WriteFile(file, []byte(canaryPass+"\n"), 0o600))

	logs := &logBuffer{}
	rig := newV2With(t, &v2Options{
		connections: "  acl: {type: valkey, addresses: [\"" + proxy.address() + "\"], username: relay, " +
			"password: {fromFile: " + file + "}, forceSingleClient: true, lookupTimeout: 300ms, " +
			"timeouts: {connect: 500ms}}\n",
		extra: resolverSection + "observability:\n  logging: {level: debug}\n" +
			"  metrics: {enabled: true, perSegmentLabels: true}\n",
		cold: true, secrets: bootstrap.LoadOptions{ReadFile: os.ReadFile},
		logger: slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	rig.flags[dynFlagsObject] = []byte(canaryFlagsYAML)
	rig.segments = map[string][]byte{segObjectName: []byte(canarySegmentsYAML)}
	rig.sets = []controlplane.FlagSet{{
		Name: setName, Sources: []string{flagsObject, dynFlagsObject}, SegmentSources: []string{segObjectName},
		APIKeys: []string{tenantKey},
	}}

	rig.publish(t)
	rig.poll(t)

	bodies := map[string]string{}
	evaluate := func(name, flag, body string) {
		code, _, out := do(t, http.MethodPost, rig.url+pathOFREPOne+flag, body, keyHeader(tenantKey))
		bodies[name] = fmt.Sprintf("%d %s", code, out)
	}

	byKey := func(id string) string { return `{"context":{"targetingKey":"` + id + `"}}` }

	// a hit, then the same id again (answered from the cache), a miss, a snapshot member, a typed attribute
	require.EventuallyWithT(t, func(collect *assert.CollectT) {
		got, err := tryEval(rig, "f-lookup", canaryMember)
		if assert.NoError(collect, err) {
			assert.True(collect, isMember(got), "%+v", got)
		}
	}, eventually, poll, "the stored member is served")

	evaluate("hit", "f-lookup", byKey(canaryMember))
	evaluate("cached", "f-lookup", byKey(canaryMember))
	evaluate("miss", "f-lookup", byKey(canaryOther))
	evaluate("snapshot", "f-snap", byKey(canarySnap))
	evaluate("typed", "f-typed", `{"context":{"targetingKey":"x","user":{"id":"`+canaryTyped+`"}}}`)
	evaluate("rejected", "f-rejected", byKey(canaryMember))

	// bulk, a flag that is not there, a body that is not valid, a request without a key
	_, _, bulk := do(t, http.MethodPost, rig.url+"/ofrep/v1/evaluate/flags", byKey(canaryMember), keyHeader(tenantKey))
	bodies["bulk"] = string(bulk)

	evaluate("unknown flag", "no-such-flag", byKey(canaryMember))
	evaluate("bad body", "f-lookup", `{"context":{"targetingKey":"`+canaryBadBody+`"`)

	noKeyCode, _, noKey := do(t, http.MethodPost, rig.url+pathOFREPOne+"f-lookup", byKey(canaryMember), nil)
	bodies["no key"] = fmt.Sprintf("%d %s", noKeyCode, noKey)

	// the store goes away: ids that were never asked have no cached answer
	proxy.setDown(true)

	require.Eventually(t, func() bool {
		status, _, out := do(
			t,
			http.MethodPost,
			rig.url+pathOFREPOne+"f-lookup",
			byKey(canaryOutage),
			keyHeader(tenantKey),
		)
		bodies["outage"] = fmt.Sprintf("%d %s", status, out)

		return strings.Contains(string(out), "STORE_ERROR")
	}, eventually, poll, "the outage answers STORE_ERROR")

	evaluate("typed outage", "f-typed", `{"context":{"targetingKey":"x","user":{"id":"`+canaryOutage+`"}}}`)

	_, _, config := do(t, http.MethodPost, rig.url+pathConfig, `{}`, keyHeader(tenantKey))
	bodies["flag configuration"] = string(config)

	_, _, info := do(t, http.MethodGet, rig.url+"/info", "", nil)
	bodies["info"] = string(info)

	families, err := rig.metrics.Registry.Gather()
	require.NoError(t, err)

	var scraped bytes.Buffer

	for _, fam := range families {
		scraped.WriteString(fam.String())
	}

	bodies["metrics"] = scraped.String()
	bodies["logs"] = logs.String()

	// the paths were taken: a hit, and a store error
	assert.Contains(t, bodies["hit"], `"value":true`)
	assert.Contains(t, bodies["miss"], `"value":false`)
	assert.Contains(t, bodies["snapshot"], `"value":true`)
	assert.Contains(t, bodies["typed"], `"value":true`)
	assert.Contains(t, bodies["rejected"], "NOT_CONFIGURED")
	assert.Contains(t, bodies["logs"], "dynamic segment definitions rejected", "the rejection is logged, without an id")
	assert.Contains(t, bodies["metrics"], "semaphore_segment_named_lookups_total", "the opt-in series are on")

	for name, out := range bodies {
		for _, secret := range []string{
			canaryMember, canaryOther, canarySnap, canaryOutage, canaryTyped, canaryBadBody, canaryPass,
		} {
			// a response may carry back nothing of the request but its own status: the ids are never echoed
			assert.NotContains(t, out, secret, name)
		}
	}
}
