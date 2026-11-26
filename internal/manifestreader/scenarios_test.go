package manifestreader_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/internal/manifestreader"
)

// okState is the state of an object that is verified content.
func okState(tb testing.TB, o obj) manifestreader.ObjectState {
	tb.Helper()

	return manifestreader.ObjectState{
		Kind: controlplane.KindFlags, Format: controlplane.FormatJSON,
		Checksum: controlplane.ChecksumOf([]byte(o.content)), State: manifestreader.StateOK, Bytes: []byte(o.content),
	}
}

func snapOf(gen uint64, objs map[string]manifestreader.ObjectState) *manifestreader.Snapshot {
	return &manifestreader.Snapshot{Generation: gen, Channel: testChannel, Objects: objs}
}

func accepted(gen uint64, objects int) manifestreader.ManifestStatus {
	return manifestreader.ManifestStatus{
		Outcome: manifestreader.OutcomeAccepted, Channel: testChannel, Generation: gen, Objects: objects,
	}
}

// step is one round of a scenario: what the publisher does before it, and what the round must do.
type step struct {
	// act changes the store before the round; nil changes nothing.
	act func(t *testing.T, store *fakeStore)
	// wantCalls gets the ETag of the manifest content the reader read last and returns the exact requests.
	wantCalls func(t *testing.T, held string) []call
	// wantSnap is the snapshot the round must publish; nil means the round must publish none.
	wantSnap *manifestreader.Snapshot
	// check asserts on the status after the round.
	check func(t *testing.T, status manifestreader.Status)
	name  string
}

// condGet is the only request of a round that changes nothing: the conditional GET of the manifest.
func condGet(_ *testing.T, held string) []call { return []call{manifestCall(held)} }

// runSteps starts a reader on generation 1 of alpha, beta and gamma, and plays the steps.
func runSteps(t *testing.T, steps []step) {
	t.Helper()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})
	require.NotNil(t, rdr.snapshot(), "the first round must publish generation 1")
	store.takeCalls()

	for _, status := range steps {
		held := manifestETag(store)

		if status.act != nil {
			status.act(t, store)
		}

		got := rdr.round()
		assert.Equal(t, sortCalls(status.wantCalls(t, held)), got, "%s: requests", status.name)
		assert.Equal(t, status.wantSnap, rdr.snapshot(), "%s: snapshot", status.name)

		if status.check != nil {
			status.check(t, rdr.reader.Status())
		}
	}
}

func TestReader_ColdStart(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	twin := obj{name: nameTwin, content: gamma.content}

	tests := []struct {
		wantCalls func(t *testing.T) []call
		wantObjs  map[string]manifestreader.ObjectState
		name      string
		objs      []obj
	}{
		{
			name: "one manifest GET and one GET per distinct checksum",
			objs: threeObjects(),
			wantCalls: func(t *testing.T) []call {
				t.Helper()

				return []call{manifestCall(""), objCall(t, alpha), objCall(t, beta), objCall(t, gamma)}
			},
			wantObjs: map[string]manifestreader.ObjectState{
				nameAlpha: okState(t, alpha), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
			},
		},
		{
			name: "two names with one checksum are fetched once",
			objs: []obj{alpha, gamma, twin},
			wantCalls: func(t *testing.T) []call {
				t.Helper()

				return []call{manifestCall(""), objCall(t, alpha), objCall(t, gamma)}
			},
			wantObjs: map[string]manifestreader.ObjectState{
				nameAlpha: okState(t, alpha), nameGamma: okState(t, gamma), nameTwin: okState(t, twin),
			},
		},
		{
			name:      "an empty manifest is one GET and an empty snapshot",
			objs:      nil,
			wantCalls: func(*testing.T) []call { return []call{manifestCall("")} },
			wantObjs:  map[string]manifestreader.ObjectState{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, tt.objs...)

			rdr := startRig(t, store, &manifestreader.Config{})

			assert.Equal(t, sortCalls(tt.wantCalls(t)), store.takeCalls())
			assert.Equal(t, snapOf(1, tt.wantObjs), rdr.snapshot())

			status := rdr.reader.Status()
			assert.Equal(t, accepted(1, len(tt.objs)), status.Manifest)
			assert.Equal(t, manifestreader.PollUpdated, status.Poll.Result)
			assert.Equal(t, manifestreader.Counts{OK: len(tt.objs)}, status.Counts)
			assert.Empty(t, status.Failures)
			assert.Equal(t, uint64(1), status.Polls)
			assert.Equal(t, uint64(0), status.NotModified)
		})
	}
}

func TestReader_SteadyStateIsOneConditionalGetPerRound(t *testing.T) {
	t.Parallel()

	const rounds = 6

	steps := make([]step, rounds)
	for i := range steps {
		steps[i] = step{
			name: "round", wantCalls: condGet,
			check: func(t *testing.T, status manifestreader.Status) {
				t.Helper()

				assert.Equal(t, manifestreader.PollNotModified, status.Poll.Result)
				assert.Equal(t, accepted(1, 3), status.Manifest)
				assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
			},
		}
	}

	runSteps(t, steps)
}

func TestReader_SteadyStateCountersAndSync(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})

	const rounds = 4
	for range rounds {
		rdr.round()
	}

	status := rdr.reader.Status()
	assert.Equal(t, uint64(rounds+1), status.Polls)
	assert.Equal(t, uint64(rounds), status.NotModified)
	assert.Equal(t, clocktestInstant(), status.Poll.At)
	assert.Equal(t, clocktestInstant(), status.LastSync)
}

func TestReader_ChangesBetweenGenerations(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	delta := obj{name: "delta", content: `{"delta":4}`}
	twin := obj{name: nameTwin, content: gamma.content}

	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "one object changed costs one manifest GET and one object GET",
			steps: []step{{
				name: "gen 2",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 2, alpha2, beta, gamma)
				},
				wantCalls: func(t *testing.T, held string) []call {
					t.Helper()

					return []call{manifestCall(held), objCall(t, alpha2)}
				},
				wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha: okState(t, alpha2), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
				}),
				check: func(t *testing.T, status manifestreader.Status) {
					t.Helper()
					assert.Equal(t, accepted(2, 3), status.Manifest)
					assert.Equal(t, manifestreader.PollUpdated, status.Poll.Result)
				},
			}, {
				name: "then quiet again", wantCalls: condGet,
			}},
		},
		{
			name: "an object added and one removed: only the added one is fetched",
			steps: []step{{
				name: "gen 2",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 2, beta, gamma, delta)
				},
				wantCalls: func(t *testing.T, held string) []call {
					t.Helper()

					return []call{manifestCall(held), objCall(t, delta)}
				},
				wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
					nameBeta: okState(t, beta), nameGamma: okState(t, gamma), "delta": okState(t, delta),
				}),
				check: func(t *testing.T, status manifestreader.Status) {
					t.Helper()
					assert.Equal(t, accepted(2, 3), status.Manifest)
					assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
				},
			}},
		},
		{
			name: "a name whose content equals a held one costs no object GET",
			steps: []step{{
				name: "gen 2 adds a twin of gamma",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 2, alpha, beta, gamma, twin)
				},
				wantCalls: func(_ *testing.T, held string) []call {
					return []call{manifestCall(held)}
				},
				wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha: okState(
						t,
						alpha,
					),
					nameBeta:  okState(t, beta),
					nameGamma: okState(t, gamma),
					nameTwin:  okState(t, twin),
				}),
			}},
		},
		{
			name: "a rollback is a higher generation of older content: the old content is fetched again",
			steps: []step{{
				name: "gen 2 changes alpha",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 2, alpha2, beta, gamma)
				},
				wantCalls: func(t *testing.T, held string) []call {
					t.Helper()

					return []call{manifestCall(held), objCall(t, alpha2)}
				},
				wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha: okState(t, alpha2), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
				}),
			}, {
				name: "gen 3 goes back to alpha 1, no longer held",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 3, alpha, beta, gamma)
				},
				wantCalls: func(t *testing.T, held string) []call {
					t.Helper()

					return []call{manifestCall(held), objCall(t, alpha)}
				},
				wantSnap: snapOf(3, map[string]manifestreader.ObjectState{
					nameAlpha: okState(t, alpha), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
				}),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			runSteps(t, tt.steps)
		})
	}
}

func TestReader_ManifestRejections(t *testing.T) {
	t.Parallel()

	alpha, beta, gamma := threeObjects()[0], threeObjects()[1], threeObjects()[2]
	alpha2 := obj{name: nameAlpha, content: contentAlphaChanged}
	garbage := []byte("{this is: [not a manifest")

	wantRejected := func(outcome, reason string, generation uint64) func(*testing.T, manifestreader.Status) {
		return func(t *testing.T, status manifestreader.Status) {
			t.Helper()

			assert.Equal(t, outcome, status.Manifest.Outcome)
			assert.Equal(t, reason, status.Manifest.Reason)
			assert.Equal(t, generation, status.Manifest.Generation, "the held generation does not change")
			assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
		}
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "invalid manifest keeps the snapshot; its ETag is kept so it is not read again; a corrected one is read",
			steps: []step{{
				name: "garbage",
				act:  func(_ *testing.T, store *fakeStore) { store.put(manifestKey, garbage) },
				wantCalls: func(_ *testing.T, held string) []call {
					return []call{manifestCall(held)}
				},
				check: wantRejected(manifestreader.OutcomeInvalid, "decode", 1),
			}, {
				name: "still garbage: a 304 against the ETag of the garbage, nothing is read or reported again",
				wantCalls: func(_ *testing.T, held string) []call {
					assert.Equal(t, etagOf(garbage), held)

					return []call{manifestCall(held)}
				},
				check: func(t *testing.T, status manifestreader.Status) {
					t.Helper()
					wantRejected(manifestreader.OutcomeInvalid, "decode", 1)(t, status)
					assert.Equal(t, manifestreader.PollNotModified, status.Poll.Result)
				},
			}, {
				name: "corrected manifest of generation 2",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 2, alpha2, beta, gamma)
				},
				wantCalls: func(t *testing.T, held string) []call {
					t.Helper()

					return []call{manifestCall(held), objCall(t, alpha2)}
				},
				wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha: okState(t, alpha2), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
				}),
				check: func(t *testing.T, status manifestreader.Status) {
					t.Helper()
					assert.Equal(t, accepted(2, 3), status.Manifest)
				},
			}},
		},
		{
			name: "lower generation is ignored and reported, a higher one is accepted after it",
			steps: []step{{
				name: "gen 2 first",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 2, alpha, beta, gamma)
				},
				wantCalls: func(_ *testing.T, held string) []call {
					return []call{manifestCall(held)}
				},
				wantSnap: snapOf(2, map[string]manifestreader.ObjectState{
					nameAlpha: okState(t, alpha), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
				}),
			}, {
				name: "gen 1 with another object",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 1, alpha2, beta, gamma)
				},
				wantCalls: func(_ *testing.T, held string) []call {
					return []call{manifestCall(held)}
				},
				check: wantRejected(manifestreader.OutcomeLowerGeneration, "generation", 2),
			}, {
				name: "gen 3",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 3, alpha2, beta, gamma)
				},
				wantCalls: func(t *testing.T, held string) []call {
					t.Helper()

					return []call{manifestCall(held), objCall(t, alpha2)}
				},
				wantSnap: snapOf(3, map[string]manifestreader.ObjectState{
					nameAlpha: okState(t, alpha2), nameBeta: okState(t, beta), nameGamma: okState(t, gamma),
				}),
			}},
		},
		{
			name: "equal generation with another checksum is a conflict: rejected, reported, snapshot kept",
			steps: []step{{
				name: "gen 1 again with alpha changed",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 1, alpha2, beta, gamma)
				},
				wantCalls: func(_ *testing.T, held string) []call {
					return []call{manifestCall(held)}
				},
				check: wantRejected(manifestreader.OutcomeConflict, "generation", 1),
			}},
		},
		{
			name: "equal generation with another object set is a conflict",
			steps: []step{{
				name: "gen 1 again without gamma",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					publish(t, store, 1, alpha, beta)
				},
				wantCalls: func(_ *testing.T, held string) []call {
					return []call{manifestCall(held)}
				},
				check: wantRejected(manifestreader.OutcomeConflict, "generation", 1),
			}},
		},
		{
			name: "equal generation with the same objects is not a conflict (a rewrite of the same content)",
			steps: []step{{
				name: "gen 1 with another createdAt",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					manifest := manifestOf(t, 1, threeObjects()...)
					at := clocktestInstant()
					manifest.CreatedAt = &at
					putManifest(t, store, manifest)
				},
				wantCalls: func(_ *testing.T, held string) []call {
					return []call{manifestCall(held)}
				},
				check: func(t *testing.T, status manifestreader.Status) {
					t.Helper()
					assert.Equal(t, accepted(1, 3), status.Manifest)
				},
			}},
		},
		{
			name: "another channel is rejected",
			steps: []step{{
				name: "channel canary",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					manifest := manifestOf(t, 2, threeObjects()...)
					manifest.Channel = "canary"
					putManifest(t, store, manifest)
				},
				wantCalls: func(_ *testing.T, held string) []call { return []call{manifestCall(held)} },
				check:     wantRejected(manifestreader.OutcomeChannelMismatch, "channel", 1),
			}},
		},
		{
			name: "an unsupported apiVersion is rejected whole",
			steps: []step{{
				name: "apiVersion v9",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					data, err := controlplane.MarshalManifest(manifestOf(t, 2, threeObjects()...))
					require.NoError(t, err)

					store.put(manifestKey, bytes.Replace(data, []byte("semaphore/v1"), []byte("semaphore/v9"), 1))
				},
				wantCalls: func(_ *testing.T, held string) []call { return []call{manifestCall(held)} },
				check:     wantRejected(manifestreader.OutcomeInvalid, "unsupported-version", 1),
			}},
		},
		{
			name: "an invalid object entry rejects the manifest whole, with the code of the problem",
			steps: []step{{
				name: "missing checksum",
				act: func(t *testing.T, store *fakeStore) {
					t.Helper()

					manifest := manifestOf(t, 2, threeObjects()...)
					manifest.Objects[1].Checksum = controlplane.Checksum{}
					putManifest(t, store, manifest)
				},
				wantCalls: func(_ *testing.T, held string) []call { return []call{manifestCall(held)} },
				check:     wantRejected(manifestreader.OutcomeInvalid, string(controlplane.CodeInvalidChecksum), 1),
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			runSteps(t, tt.steps)
		})
	}
}

func TestReader_ManifestRejectionIsReportedNotRetriedUntilTheBytesChange(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})
	require.NotNil(t, rdr.snapshot())

	good := manifestETag(store)
	store.put(manifestKey, []byte("not: [a manifest"))
	bad := manifestETag(store)

	assert.Equal(t, []call{manifestCall(good)}, rdr.round(), "the bad manifest is read once")
	assert.Equal(t, []call{manifestCall(bad)}, rdr.round(), "and then answered with a 304 against its own ETag")
	assert.Equal(t, []call{manifestCall(bad)}, rdr.round())

	// the publisher puts the previous bytes back: the reader holds the same generation, nothing changes
	publish(t, store, 1, threeObjects()...)
	assert.Equal(t, []call{manifestCall(bad)}, rdr.round())
	assert.Nil(t, rdr.snapshot())
	assert.Equal(t, accepted(1, 3), rdr.reader.Status().Manifest)
}

func TestReader_UnknownFieldsAreCounted(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	manifest := manifestOf(t, 1, threeObjects()...)

	for _, o := range threeObjects() {
		store.put(keyOf(t, o), []byte(o.content))
	}

	data, err := controlplane.MarshalManifestJSON(manifest)
	require.NoError(t, err)

	data = append([]byte(`{"futureField":1,`), data[1:]...)
	store.put(manifestKey, data)

	rdr := startRig(t, store, &manifestreader.Config{})

	assert.NotNil(t, rdr.snapshot())
	assert.Equal(t, 1, rdr.reader.Status().Manifest.UnknownFields)
}

func TestReader_ManifestLimits(t *testing.T) {
	t.Parallel()

	many := func(n int) []obj {
		objs := make([]obj, n)
		for i := range objs {
			objs[i] = obj{name: "o" + string(rune('a'+i)), content: `{"n":` + string(rune('a'+i)) + `}`}
		}

		return objs
	}

	tests := []struct {
		name         string
		wantReason   string
		wantPoll     string
		objs         []obj
		cfg          manifestreader.Config
		wantObjCalls int
		wantSnapshot bool
	}{
		{
			name: "exactly at max objects is accepted", cfg: manifestreader.Config{MaxObjects: 5},
			objs: many(5), wantSnapshot: true, wantPoll: manifestreader.PollUpdated, wantObjCalls: 5,
		},
		{
			name: "one over max objects is rejected whole, no object is fetched",
			cfg:  manifestreader.Config{MaxObjects: 5}, objs: many(6),
			wantReason: string(controlplane.CodeTooManyObjects), wantPoll: manifestreader.PollUpdated,
		},
		{
			name: "a manifest over the byte limit is not read", cfg: manifestreader.Config{MaxManifestBytes: 64},
			objs: many(3), wantPoll: manifestreader.PollTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, tt.objs...)

			rdr := startRig(t, store, &tt.cfg)

			assert.Equal(t, tt.wantSnapshot, rdr.snapshot() != nil)
			assert.Len(t, store.takeCalls(), 1+tt.wantObjCalls)

			status := rdr.reader.Status()
			assert.Equal(t, tt.wantPoll, status.Poll.Result)

			if tt.wantReason != "" {
				assert.Equal(t, manifestreader.OutcomeInvalid, status.Manifest.Outcome)
				assert.Equal(t, tt.wantReason, status.Manifest.Reason)
			}
		})
	}
}

func TestReader_ManifestReadErrorsKeepThePreviousSnapshot(t *testing.T) {
	t.Parallel()

	transient := &manifestreader.FetchError{Err: errTransient, Reason: manifestreader.ReasonError, Retryable: true}
	denied := &manifestreader.FetchError{Err: errDenied, Reason: manifestreader.ReasonDenied}

	tests := []struct {
		name       string
		act        func(store *fakeStore)
		wantResult string
		wantReason string
	}{
		{
			name:       "the manifest is deleted",
			act:        func(store *fakeStore) { store.remove(manifestKey) },
			wantResult: manifestreader.PollNotFound, wantReason: manifestreader.ReasonMissing,
		},
		{
			name:       "a transient error",
			act:        func(store *fakeStore) { store.failNext(manifestKey, transient) },
			wantResult: manifestreader.PollError, wantReason: manifestreader.ReasonError,
		},
		{
			name:       "access denied",
			act:        func(store *fakeStore) { store.failNext(manifestKey, denied) },
			wantResult: manifestreader.PollError, wantReason: manifestreader.ReasonDenied,
		},
		{
			name:       "an unclassified error",
			act:        func(store *fakeStore) { store.failNext(manifestKey, errUnclassified) },
			wantResult: manifestreader.PollError, wantReason: manifestreader.ReasonError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Helper()

			t.Parallel()

			store := newFakeStore()
			publish(t, store, 1, threeObjects()...)

			rdr := startRig(t, store, &manifestreader.Config{})
			require.NotNil(t, rdr.snapshot())

			held := manifestETag(store)
			tt.act(store)

			assert.Equal(t, []call{manifestCall(held)}, rdr.round(), "one request, no object request")
			assert.Nil(t, rdr.snapshot())

			status := rdr.reader.Status()
			assert.Equal(t, tt.wantResult, status.Poll.Result)
			assert.Equal(t, tt.wantReason, status.Poll.Reason)
			assert.Equal(t, accepted(1, 3), status.Manifest, "the previous manifest is still the held one")
			assert.Equal(t, manifestreader.Counts{OK: 3}, status.Counts)
		})
	}
}

func TestReader_ManifestErrorThenRecovery(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	publish(t, store, 1, threeObjects()...)

	rdr := startRig(t, store, &manifestreader.Config{})
	require.NotNil(t, rdr.snapshot())

	held := manifestETag(store)
	store.remove(manifestKey)

	assert.Equal(t, []call{manifestCall(held)}, rdr.round())

	alpha2 := obj{name: nameAlpha, content: `{"alpha":"after the outage"}`}
	publish(t, store, 2, alpha2, threeObjects()[1], threeObjects()[2])

	assert.Equal(t, []call{manifestCall(held), objCall(t, alpha2)}, rdr.round(), "the old ETag is still sent")
	require.NotNil(t, rdr.snapshot())
	assert.Equal(t, manifestreader.PollUpdated, rdr.reader.Status().Poll.Result)
	assert.Empty(t, rdr.reader.Status().Poll.Reason)
}
