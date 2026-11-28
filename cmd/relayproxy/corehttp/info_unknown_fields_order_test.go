package corehttp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// unknownFlagFile is a flag file with one flag that carries count unknown fields, named by prefix.
func unknownFlagFile(flag, prefix string, count int) []byte {
	var doc strings.Builder

	doc.WriteString(
		flag + ":\n  variations: {enabled: true, disabled: false}\n  defaultRule: {name: default, variation: enabled}\n",
	)

	for i := range count {
		fmt.Fprintf(&doc, "  %s%02d: 1\n", prefix, i)
	}

	return []byte(doc.String())
}

func configInfoOf(t *testing.T, base string) *model.InfoResponse {
	t.Helper()

	code, _, body := do(t, http.MethodGet, base+"/info", "", nil)
	require.Equal(t, http.StatusOK, code)

	var info model.InfoResponse
	require.NoError(t, json.Unmarshal(body, &info))
	require.NotNil(t, info.Config)

	return &info
}

// The fields of /info are sorted as strings, prefix included, and the limit of ten applies to that order: object
// "a-b" sorts before object "a" ("-" is before "/"), although "a" is the shorter name.
func TestInfo_UnknownFieldsAreSortedAfterThePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantFields []string
		countA     int
		countAB    int
		wantCount  int
	}{
		{
			name: "one field each", countA: 1, countAB: 1, wantCount: 2,
			wantFields: []string{"a-b/flag-ab.f00", "a/flag-a.f00"},
		},
		{
			name: "the limit cuts the sorted list", countA: 8, countAB: 8, wantCount: 16,
			wantFields: []string{
				"a-b/flag-ab.f00", "a-b/flag-ab.f01", "a-b/flag-ab.f02", "a-b/flag-ab.f03", "a-b/flag-ab.f04",
				"a-b/flag-ab.f05", "a-b/flag-ab.f06", "a-b/flag-ab.f07", "a/flag-a.f00", "a/flag-a.f01",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newV2With(t, &v2Options{cold: true})
			rig.flags = map[string][]byte{
				"a":   unknownFlagFile("flag-a", "f", tt.countA),
				"a-b": unknownFlagFile("flag-ab", "f", tt.countAB),
			}
			rig.sets = []controlplane.FlagSet{
				{Name: setName, Sources: []string{"a", "a-b"}, APIKeys: []string{tenantKey}},
			}
			rig.publish(t)
			rig.poll(t)

			info := configInfoOf(t, rig.url)
			assert.Equal(t, []model.UnknownFieldsInfo{
				{Kind: controlplane.KindFlags, Fields: tt.wantFields, Count: tt.wantCount},
			}, info.Config.UnknownFields)
		})
	}
}

// After a restart from the state directory the relay serves the restored documents, and /info describes them: the
// unknown fields of the manifest and of its objects are there before any poll of the store.
func TestInfo_UnknownFieldsSurviveARestartFromTheStateDirectory(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	first := newV2With(t, &v2Options{stateDir: stateDir, cold: true})

	first.flags[flagsObject] = unknownFlagFile("bool-flag", "future", 1)
	raw := nextManifestBytes(t, first)
	publishManifest(first, append(raw, []byte("extraManifest: 1\n")...))
	first.poll(t)

	want := []model.UnknownFieldsInfo{
		{Kind: controlplane.KindFlags, Fields: []string{flagsObject + "/bool-flag.future00"}, Count: 1},
		{Kind: strings.ToLower(controlplane.DocumentKindManifest), Fields: []string{"extraManifest"}, Count: 1},
	}
	require.Equal(t, want, configInfoOf(t, first.url).Config.UnknownFields)

	ctx, cancel := context.WithTimeout(t.Context(), waitBound)
	defer cancel()

	require.NoError(t, first.core.Shutdown(ctx))

	// the store is empty for the second start: only the state directory has the documents
	second := newV2With(t, &v2Options{stateDir: stateDir, cold: true, dir: filepath.Join(t.TempDir(), "empty")})
	second.awaitGeneration(t, 1)

	require.Equal(t, manifestreader.RestoreRestored, second.core.Status().Source.Reader.State.Restore)
	assert.Equal(t, want, configInfoOf(t, second.url).Config.UnknownFields)
}
