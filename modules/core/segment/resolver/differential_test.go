package resolver_test

import (
	"context"
	"math/rand/v2"
	"os"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/resolver"
)

const (
	stressSeed         = 20260726
	stressIDs          = 64
	stressWorkers      = 8
	defaultStressLoops = 300
)

// truth is what the store knows: an id is a member of a segment when its number is even (beta) or a multiple of
// three (gamma). Nothing changes it, so a resolver that answers must answer exactly this, or say Unknown.
func truth(seg string, number int) segment.State {
	member := false

	switch seg {
	case segBeta:
		member = number%2 == 0
	case segGamma:
		member = number%3 == 0
	}

	if member {
		return segment.Member
	}

	return segment.NotMember
}

func stressStore() *fakeStore {
	data := map[string]map[string]bool{segBeta: {}, segGamma: {}}

	for number := range stressIDs {
		id := "id" + strconv.Itoa(number)
		data[segBeta][id] = number%2 == 0
		data[segGamma][id] = number%3 == 0
	}

	return &fakeStore{data: data}
}

// TestDifferentialAgainstTheTruth hammers the resolver with random batches, failing stores, cancelled contexts and
// invalidations from several goroutines. Whatever it answers must be what the store knows. The number of rounds of
// each worker is SEGMENT_STRESS_ITERATIONS (a few hundred by default); the seed is fixed.
func TestDifferentialAgainstTheTruth(t *testing.T) {
	t.Parallel()

	loops := defaultStressLoops

	if env := os.Getenv("SEGMENT_STRESS_ITERATIONS"); env != "" {
		parsed, err := strconv.Atoi(env)
		require.NoError(t, err)

		loops = parsed
	}

	store := stressStore()
	res := newResolver(t, store, &resolver.Options{
		Default: resolver.SegmentOptions{MaxEntries: 40, Shards: 4, NoBreaker: true},
	})

	wrong := make([][]string, stressWorkers)

	var wg sync.WaitGroup

	for worker := range stressWorkers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(stressSeed, uint64(worker)))

			for range loops {
				wrong[worker] = append(wrong[worker], stressRound(rng, res, store, worker)...)
			}
		})
	}

	wg.Wait()

	for worker := range stressWorkers {
		assert.Empty(t, wrong[worker], "worker %d", worker)
	}
}

// stressRound does one random operation and returns the answers that are wrong.
func stressRound(rng *rand.Rand, res *resolver.Resolver, store *fakeStore, worker int) []string {
	switch rng.IntN(12) {
	case 0:
		res.Invalidate([]string{segBeta, segGamma}[rng.IntN(2)])

		return nil
	case 1:
		if worker == 0 { // one worker flips the store between failing and healthy
			store.Fail()
		}

		return nil
	case 2:
		if worker == 0 {
			store.Heal()
		}

		return nil
	}

	lookups := make([]segment.Lookup, 1+rng.IntN(6))
	numbers := make([]int, len(lookups))

	for i := range lookups {
		numbers[i] = rng.IntN(stressIDs)
		lookups[i] = lk([]string{segBeta, segGamma}[rng.IntN(2)], "id"+strconv.Itoa(numbers[i]))
	}

	ctx, cancel := context.WithCancel(context.Background())
	if rng.IntN(8) == 0 {
		cancel() // some callers are gone before they start
	}

	defer cancel()

	_ = res.Contains(ctx, lookups)

	var wrong []string

	for i := range lookups {
		if got := lookups[i].State; got != segment.Unknown && got != truth(lookups[i].Segment, numbers[i]) {
			wrong = append(wrong, lookups[i].Segment+":"+lookups[i].ID+" is "+got.String())
		}
	}

	return wrong
}
