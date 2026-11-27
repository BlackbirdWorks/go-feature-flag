package valkeysegment_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

// benchClient answers from constants and records nothing, so the benchmark counts the allocations of the
// provider plus the one Cmd per command that go-redis also allocates.
type benchClient struct{}

func (benchClient) Pipeline() redis.Pipeliner { //nolint:ireturn // the Client interface returns it
	return &benchPipe{}
}

type benchPipe struct {
	redis.Pipeliner

	cmds []*redis.Cmd
}

func (prov *benchPipe) Do(ctx context.Context, args ...any) *redis.Cmd {
	cmd := redis.NewCmd(ctx, args...)
	prov.cmds = append(prov.cmds, cmd)

	return cmd
}

func (prov *benchPipe) Exec(context.Context) ([]redis.Cmder, error) {
	for _, cmd := range prov.cmds {
		if cmd.Args()[0] == cmdExists {
			cmd.SetVal(int64(1))

			continue
		}

		cmd.SetVal(make([]any, len(cmd.Args())-2))
	}

	return nil, nil
}

// BenchmarkContains measures the allocations of the provider with a client that answers at once. The pipeline of
// a real client allocates on top of this.
func BenchmarkContains(b *testing.B) {
	for _, cnt := range []int{1, 8} {
		b.Run(strconv.Itoa(cnt)+"lookups", func(b *testing.B) {
			prov, err := valkeysegment.New(benchClient{}, valkeysegment.Options{Segments: twoSegments()})
			require.NoError(b, err)

			lookups := make([]segment.Lookup, cnt)
			for i := range lookups {
				lookups[i] = segment.Lookup{Segment: segBeta, ID: "id" + strconv.Itoa(i)}
			}

			ctx := context.Background()

			b.ReportAllocs()

			for b.Loop() {
				_ = prov.Contains(ctx, lookups)
			}
		})
	}
}
