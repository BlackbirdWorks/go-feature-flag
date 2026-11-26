package s3publish_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/testhelpers/s3fake"
)

// barrierWait bounds how long a publisher waits for the other one at the barrier.
const barrierWait = 10 * time.Second

// barrier releases its waiters once n of them arrived.
type barrier struct {
	release chan struct{}
	n       int
	arrived int
	mu      sync.Mutex
}

func newBarrier(n int) *barrier { return &barrier{n: n, release: make(chan struct{})} }

func (b *barrier) wait(ctx context.Context) error {
	b.mu.Lock()
	b.arrived++

	if b.arrived == b.n {
		close(b.release)
	}

	b.mu.Unlock()

	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(barrierWait):
		return context.DeadlineExceeded
	}
}

// gatedAPI makes every reader of the manifest wait, after it has read, until the gate opens.
type gatedAPI struct {
	s3publish.API

	gate *barrier
	mu   sync.Mutex
}

func (g *gatedAPI) GetObject(
	ctx context.Context,
	in *s3.GetObjectInput,
	opts ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	out, err := g.API.GetObject(ctx, in, opts...)

	if aws.ToString(in.Key) == manifestKey {
		g.mu.Lock()
		b := g.gate
		g.mu.Unlock()

		waitErr := b.wait(ctx)
		if waitErr != nil {
			return nil, waitErr
		}
	}

	return out, err
}

func (g *gatedAPI) setGate(b *barrier) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.gate = b
}

func TestTwoPublishersOnOneBucket(t *testing.T) {
	t.Parallel()

	const rounds = 6

	fake := s3fake.New(manifestKey)
	gated := &gatedAPI{API: fake}
	pubA := newPublisher(t, gated, nil)
	pubB := newPublisher(t, gated, nil)

	var winners []uint64

	for round := range rounds {
		gated.setGate(newBarrier(2))

		results := make([]*s3publish.Result, 2)
		errs := make([]error, 2)

		docs := [][]s3publish.Object{
			standardObjects(t, "a"+string(rune('0'+round))), standardObjects(t, "b"+string(rune('0'+round))),
		}

		var wg sync.WaitGroup

		for i, pub := range []*s3publish.Publisher{pubA, pubB} {
			wg.Go(func() {
				results[i], errs[i] = pub.Publish(t.Context(), s3publish.Request{Objects: docs[i]})
			})
		}

		wg.Wait()

		wins, conflicts := 0, 0

		for i := range errs {
			if errs[i] == nil {
				wins++

				winners = append(winners, results[i].Generation)

				continue
			}

			if errors.Is(errs[i], s3publish.ErrConflict) {
				conflicts++
			}
		}

		require.Equal(t, 1, wins, "round %d: exactly one publisher wins", round)
		require.Equal(t, 1, conflicts, "round %d: the other gets ErrConflict", round)

		stored, ok := fake.Body(manifestKey)
		require.True(t, ok)

		manifest, _, err := controlplane.UnmarshalManifest(stored)
		require.NoError(t, err)
		assert.Equal(t, uint64(round+1), manifest.Generation, "round %d: the generation grew by one", round)

		for _, o := range manifest.Objects {
			_, found := fake.Body(o.Key)
			require.True(t, found, "round %d: manifest lists %s which is missing", round, o.Key)
		}
	}

	assert.Equal(t, []uint64{1, 2, 3, 4, 5, 6}, winners, "every generation was won exactly once")
	fake.AssertConsistent(t)
}

func TestManyPublishersNeverBreakTheBucket(t *testing.T) {
	t.Parallel()

	const publishers = 8

	fake := s3fake.New(manifestKey)
	pubs := make([]*s3publish.Publisher, publishers)
	docs := make([][]s3publish.Object, publishers)

	for i := range publishers {
		pubs[i] = newPublisher(t, fake, nil)
		docs[i] = standardObjects(t, string(rune('a'+i)))
	}

	var wg sync.WaitGroup

	wonAt := make([]uint64, publishers) // generation won by publisher i, 0 when it lost

	for i := range publishers {
		wg.Go(func() {
			res, err := pubs[i].Publish(t.Context(), s3publish.Request{Objects: docs[i]})
			if err != nil {
				assert.ErrorIs(t, err, s3publish.ErrConflict)

				return
			}

			wonAt[i] = res.Generation
		})
	}

	wg.Wait()

	var generations []uint64

	for _, generation := range wonAt {
		if generation != 0 {
			generations = append(generations, generation)
		}
	}

	require.NotEmpty(t, generations, "at least one publisher wins")
	assert.Len(t, generations, len(uniq(generations)), "a generation is won once")
	fake.AssertConsistent(t)
}

func uniq(in []uint64) map[uint64]struct{} {
	out := map[uint64]struct{}{}
	for _, v := range in {
		out[v] = struct{}{}
	}

	return out
}
