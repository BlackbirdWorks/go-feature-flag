package manifestreader_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

const (
	nameEpsilon              = "epsilon"
	unsupportedVersionReason = string(controlplane.CodeUnsupportedVersion)
)

type versionTableCase struct {
	wantSnap func(t *testing.T) *manifestreader.Snapshot
	act      func(t *testing.T, store *fakeStore)
	check    func(t *testing.T, status manifestreader.Status)
	name     string
	cfg      manifestreader.Config
	cold     bool
}

func runVersionTableCases(t *testing.T, tests []versionTableCase) {
	t.Helper()

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeStore()
			if !tt.cold {
				publish(t, store, 1, threeObjects()...)
			}

			cfg := tt.cfg

			rdr := startRig(t, store, &cfg)
			if !tt.cold {
				require.NotNil(t, rdr.snapshot())
				store.takeCalls()
			}

			tt.act(t, store)

			_ = rdr.round()
			assert.Equal(t, tt.wantSnap(t), rdr.snapshot())

			if tt.check != nil {
				tt.check(t, rdr.reader.Status())
			}
		})
	}
}

func missingUnsupportedState(t *testing.T, item obj) manifestreader.ObjectState {
	t.Helper()

	return manifestreader.ObjectState{
		Kind:     controlplane.KindFlags,
		Format:   controlplane.FormatJSON,
		Checksum: controlplane.ChecksumOf([]byte(item.content)),
		State:    manifestreader.StateMissing,
		Reason:   unsupportedVersionReason,
	}
}

func TestReader_UnsupportedObjectVersion_DegradesEntry(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	alphaNew := obj{name: nameAlpha, content: contentAlphaChanged}
	betaNew := obj{name: nameBeta, content: `{"beta":"updated"}`}
	epsilon := obj{name: nameEpsilon, content: `{"epsilon":4}`}
	twin := obj{name: nameTwin, content: alpha.content}

	runVersionTableCases(t, []versionTableCase{
		{
			name: "existing object upgraded to v2 degrades stale while sibling updates",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				store.put(keyOf(t, betaNew), []byte(betaNew.content))

				manifest := manifestOf(t, 2, alphaNew, betaNew, gamma)
				manifest.Objects[0].Version = 2
				putManifest(t, store, manifest)
			},
			wantSnap: func(t *testing.T) *manifestreader.Snapshot {
				t.Helper()

				return snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha: staleAlpha(t, unsupportedVersionReason),
					nameBeta:  okState(t, betaNew),
					nameGamma: okState(t, gamma),
				})
			},
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, accepted(2, 3), status.Manifest)
				assert.Equal(t, manifestreader.Counts{OK: 2, Stale: 1}, status.Counts)
				assert.Equal(t, []manifestreader.ObjectFailure{{
					Name:     nameAlpha,
					State:    manifestreader.StateStale,
					Reason:   unsupportedVersionReason,
					Checksum: controlplane.ChecksumOf([]byte(alphaNew.content)).Value,
				}}, status.Failures)
			},
		},
		{
			name: "new object with v2 is missing with unsupported-version and zero GET calls",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				store.put(keyOf(t, epsilon), []byte(epsilon.content))

				manifest := manifestOf(t, 2, alpha, beta, gamma, epsilon)
				manifest.Objects[3].Version = 2
				putManifest(t, store, manifest)
			},
			wantSnap: func(t *testing.T) *manifestreader.Snapshot {
				t.Helper()

				return snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha:   okState(t, alpha),
					nameBeta:    okState(t, beta),
					nameGamma:   okState(t, gamma),
					nameEpsilon: missingUnsupportedState(t, epsilon),
				})
			},
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, accepted(2, 4), status.Manifest)
				assert.Equal(t, manifestreader.Counts{OK: 3, Missing: 1}, status.Counts)
				assert.Equal(t, []manifestreader.ObjectFailure{{
					Name:     nameEpsilon,
					State:    manifestreader.StateMissing,
					Reason:   unsupportedVersionReason,
					Checksum: controlplane.ChecksumOf([]byte(epsilon.content)).Value,
				}}, status.Failures)
			},
		},
		{
			name: "v2 object sharing checksum with v1 sibling never borrows sibling bytes",
			cold: true,
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				altKey := keyPrefix + "alt/twin.json"

				store.put(keyOf(t, alpha), []byte(alpha.content))
				store.put(altKey, []byte(alpha.content))

				manifest := manifestOf(t, 1, alpha, twin)
				manifest.Objects[1].Key = altKey
				manifest.Objects[1].Version = 2
				putManifest(t, store, manifest)
			},
			wantSnap: func(t *testing.T) *manifestreader.Snapshot {
				t.Helper()

				return snapOf(1, map[string]manifestreader.ObjectState{
					nameAlpha: okState(t, alpha),
					nameTwin:  missingUnsupportedState(t, twin),
				})
			},
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, accepted(1, 2), status.Manifest)
				assert.Equal(t, manifestreader.Counts{OK: 1, Missing: 1}, status.Counts)
			},
		},
	})
}

func TestReader_UnsupportedObjectVersion_BudgetAndRejection(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	betaNew := obj{name: nameBeta, content: `{"beta":"updated"}`}

	runVersionTableCases(t, []versionTableCase{
		{
			name: "v2 object whose size exceeds MaxTotalBytes does not fail withinTotal",
			cfg:  manifestreader.Config{MaxObjectBytes: 1 << 20, MaxTotalBytes: 64},
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				store.put(keyOf(t, betaNew), []byte(betaNew.content))

				manifest := manifestOf(t, 2, alpha, betaNew, gamma)
				manifest.Objects[0].Version = 2
				manifest.Objects[0].Size = 1 << 19
				putManifest(t, store, manifest)
			},
			wantSnap: func(t *testing.T) *manifestreader.Snapshot {
				t.Helper()

				return snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha: staleAlpha(t, unsupportedVersionReason),
					nameBeta:  okState(t, betaNew),
					nameGamma: okState(t, gamma),
				})
			},
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, accepted(2, 3), status.Manifest)
			},
		},
		{
			name: "v2 object mixed with another manifest error still rejects whole manifest with the other code",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				manifest := manifestOf(t, 2, alpha, beta, gamma)
				manifest.Objects[0].Version = 2
				manifest.Objects[1].Checksum = controlplane.Checksum{}
				putManifest(t, store, manifest)
			},
			wantSnap: func(*testing.T) *manifestreader.Snapshot { return nil },
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, manifestreader.OutcomeInvalid, status.Manifest.Outcome)
				assert.Equal(t, string(controlplane.CodeInvalidChecksum), status.Manifest.Reason)
				assert.Equal(t, uint64(1), status.Manifest.Generation)
			},
		},
		{
			name: "newer manifest apiVersion still rejects whole manifest",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				data := mustMarshal(t, manifestOf(t, 2, threeObjects()...))
				store.put(manifestKey, replaceOnce(data, "semaphore/v1", "semaphore/v2"))
			},
			wantSnap: func(*testing.T) *manifestreader.Snapshot { return nil },
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, manifestreader.OutcomeInvalid, status.Manifest.Outcome)
				assert.Equal(t, unsupportedVersionReason, status.Manifest.Reason)
				assert.Equal(t, uint64(1), status.Manifest.Generation)
			},
		},
	})
}

func TestReader_UnsupportedObjectVersion_Lifecycle(t *testing.T) {
	t.Parallel()

	beta, gamma := threeObjects()[1], threeObjects()[2]
	alphaV2 := obj{name: nameAlpha, content: contentAlphaChanged}
	alphaV1 := obj{name: nameAlpha, content: contentAlphaSecond}

	runSteps(t, []step{
		{
			name: "gen 2 puts alpha on version 2",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				manifest := manifestOf(t, 2, alphaV2, beta, gamma)
				manifest.Objects[0].Version = 2
				putManifest(t, store, manifest)
			},
			wantCalls: condGet,
			wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
				nameAlpha: staleAlpha(t, unsupportedVersionReason),
				nameBeta:  okState(t, beta),
				nameGamma: okState(t, gamma),
			}),
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, accepted(2, 3), status.Manifest)
				assert.Equal(t, manifestreader.Counts{OK: 2, Stale: 1}, status.Counts)
			},
		},
		{
			name: "equal generation toggling 1 vs 2 conflicts",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				manifest := manifestOf(t, 2, alphaV2, beta, gamma)
				manifest.Objects[0].Version = 1
				putManifest(t, store, manifest)
			},
			wantCalls: condGet,
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, manifestreader.OutcomeConflict, status.Manifest.Outcome)
				assert.Equal(t, "generation", status.Manifest.Reason)
				assert.Equal(t, uint64(2), status.Manifest.Generation)
			},
		},
		{
			name: "gen 3 returns alpha to version 1 and recovers",
			act: func(t *testing.T, store *fakeStore) {
				t.Helper()

				publish(t, store, 3, alphaV1, beta, gamma)
			},
			wantCalls: func(t *testing.T, held string) []call {
				t.Helper()

				return []call{manifestCall(held), objCall(t, alphaV1)}
			},
			wantSnap: snapOf(3, map[string]manifestreader.ObjectState{
				nameAlpha: okState(t, alphaV1),
				nameBeta:  okState(t, beta),
				nameGamma: okState(t, gamma),
			}),
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, accepted(3, 3), status.Manifest)
				assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
				assert.Empty(t, status.Failures)
			},
		},
	})
}

func TestReader_UnsupportedObjectVersion_EqualGenZeroAndOne(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})
	require.NotNil(t, rdr.snapshot())

	held := manifestETag(store)
	manifest := manifestOf(t, 1, threeObjects()...)
	manifest.Objects[0].Version = controlplane.ObjectVersion1
	putManifest(t, store, manifest)

	assert.Equal(t, []call{manifestCall(held)}, rdr.round())
	assert.Nil(t, rdr.snapshot())
	assert.Equal(t, manifestreader.OutcomeAccepted, rdr.reader.Status().Manifest.Outcome)
}

func TestRestart_UnsupportedObjectVersionRestoresStale(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	objs := threeObjects()
	publish(t, store, 1, objs...)

	dir := filepath.Join(t.TempDir(), "state")

	first, state := startWithState(t, store, dir, nil)
	require.NotNil(t, first.snapshot())

	next := manifestOf(t, 2, objs...)
	next.Objects[0].Version = 2
	putManifest(t, store, next)

	first.round()
	state.flush()

	snap := first.snapshot()
	require.NotNil(t, snap)
	assert.Equal(t, staleAlpha(t, unsupportedVersionReason), snap.Objects[nameAlpha])

	first.stop()
	state.close()

	rdr, restarted := startWithState(t, store, dir, nil)
	restored := rdr.snapshot()
	require.NotNil(t, restored)
	assert.Equal(t, staleAlpha(t, unsupportedVersionReason), restored.Objects[nameAlpha])
	assert.Equal(t, manifestreader.Counts{OK: 2, Stale: 1}, rdr.reader.Status().Counts)

	restarted.flush()
	rdr.stop()
	restarted.close()
}
