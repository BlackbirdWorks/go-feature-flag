package relaycore_test

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
)

func TestCore_UnknownFieldsLoggedOnChange(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "control")
	require.NoError(t, os.MkdirAll(dir, 0o750))

	logs := &syncBuffer{}
	deps, harness := newDeps(t, secretOptions(map[string]string{hookEnv: deadURL}))
	deps.Logger = slog.New(slog.NewTextHandler(logs, nil))

	put := filePut(t, dir)

	const secretValue = "never-log-this-secret"

	// Put >10 unknown fields on flagSets, manifest, and across two flags objects to cover path capping.
	var setsExtra strings.Builder
	for i := range 12 {
		fmt.Fprintf(&setsExtra, "extraSet%02d: %s\n", i, secretValue)
	}

	const (
		testSetName = "alpha"
		testFlagKey = "test-key-unknown"
	)

	setsBytes := []byte("apiVersion: semaphore/v1\nkind: FlagSet\n" + setsExtra.String() +
		"flagSets:\n  - name: " + testSetName + "\n    sources: [" + objA + ", " + objB + "]\n" +
		"    apiKeys: [" + testFlagKey + "]\n")

	var flagsAExtra, flagsBExtra strings.Builder
	for i := range 6 {
		fmt.Fprintf(&flagsAExtra, "  extraFlagA%02d: %s\n", i, secretValue)
		fmt.Fprintf(&flagsBExtra, "  extraFlagB%02d: %s\n", i, secretValue)
	}

	flagsABytes := []byte(flagName + ":\n" + flagsAExtra.String() +
		"  variations:\n    v: 1\n  defaultRule:\n    variation: v\n")
	flagsBBytes := []byte("g:\n" + flagsBExtra.String() +
		"  variations:\n    v: 1\n  defaultRule:\n    variation: v\n")

	publishDoc := func(gen uint64, manifestExtras int, sBytes, fABytes, fBBytes []byte) {
		t.Helper()

		manifest := &controlplane.Manifest{
			APIVersion: controlplane.APIVersion,
			Kind:       controlplane.DocumentKindManifest,
			Channel:    channelName,
			Generation: gen,
		}

		add := func(name, kind string, data []byte) {
			sum := controlplane.ChecksumOf(data)
			key, err := controlplane.ObjectKey(sum, controlplane.FormatYAML)
			require.NoError(t, err)
			put(key, data)

			manifest.Objects = append(manifest.Objects, controlplane.ManifestObject{
				Name: name, Kind: kind, Connection: connName, Key: key,
				Format: controlplane.FormatYAML, Checksum: sum, Size: int64(len(data)),
			})
		}

		add(setsName, controlplane.KindFlagSets, sBytes)
		add(objA, controlplane.KindFlags, fABytes)

		if len(fBBytes) > 0 {
			add(objB, controlplane.KindFlags, fBBytes)
		}

		rawManifest, err := controlplane.MarshalManifest(manifest)
		require.NoError(t, err)

		var extra strings.Builder
		for i := range manifestExtras {
			fmt.Fprintf(&extra, "extraManifest%02d: %s\n", i, secretValue)
		}

		put(manifestKey, append(rawManifest, []byte(extra.String())...))
	}

	publishDoc(1, 12, setsBytes, flagsABytes, flagsBBytes)

	cfg := parse(t, fileDoc(dir, ""), bootstrap.LoadOptions{LookupEnv: deps.Secrets.LookupEnv})
	harness.start(t, cfg, deps)
	harness.settle(t)
	harness.awaitGeneration(t, 1)

	text := logs.String()
	assert.NotContains(t, text, secretValue)
	assert.Equal(t, 3, strings.Count(text, `msg="unknown fields in control-plane documents"`))
	assert.Contains(t, text, `kind=flagSets count=12`)
	assert.Contains(t, text, `kind=flags count=12`)
	assert.Contains(t, text, `kind=manifest count=12`)
	assert.Contains(t, text, `sets/extraSet09`)
	assert.NotContains(t, text, `sets/extraSet10`)
	assert.Contains(t, text, objA+`/f.extraFlagA05`)
	assert.Contains(t, text, objB+`/g.extraFlagB03`)
	assert.NotContains(t, text, objB+`/g.extraFlagB04`)

	// Unchanged unknown-field counts in generation 2 do not emit duplicate warning logs.
	flagsAV2 := []byte(flagName + ":\n" + flagsAExtra.String() +
		"  variations:\n    v: 2\n  defaultRule:\n    variation: v\n")
	publishDoc(2, 12, setsBytes, flagsAV2, flagsBBytes)
	harness.poll(t)
	harness.awaitGeneration(t, 2)

	assert.Equal(t, 3, strings.Count(logs.String(), `msg="unknown fields in control-plane documents"`))

	// Removing all unknown fields in generation 3 logs cleared once per previously non-zero kind.
	cleanSets, err := controlplane.MarshalFlagSet(&controlplane.FlagSetDocument{
		FlagSets: []controlplane.FlagSet{
			{Name: testSetName, Sources: []string{objA}, APIKeys: []string{testFlagKey}},
		},
	})
	require.NoError(t, err)
	publishDoc(3, 0, cleanSets, flagsYAML(3), nil)
	harness.poll(t)
	harness.awaitGeneration(t, 3)

	finalLogs := logs.String()
	assert.Equal(t, 3, strings.Count(finalLogs, `msg="unknown fields cleared in control-plane documents"`))
}
