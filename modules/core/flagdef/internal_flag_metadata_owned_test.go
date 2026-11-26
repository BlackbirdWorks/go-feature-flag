package flagdef_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

const (
	ownedUserKey     = "user"
	ownedRaceWorkers = 8
	ownedRaceRounds  = 200
)

func ownedMetadata() map[string]any {
	return map[string]any{
		"owner": "team-a",
		"links": map[string]any{"docs": "https://example.com/docs"},
	}
}

// the metadata an evaluation returns belongs to the caller: whatever the path of the evaluation, the caller
// can add, change and delete keys without changing the flag, and without racing with the other evaluations.
func TestInternalFlag_Value_MetadataBelongsToTheCaller(t *testing.T) {
	t.Parallel()

	variations := func() *map[string]*any {
		return &map[string]*any{"on": new(any("on")), "off": new(any("off"))}
	}

	tests := []struct {
		flag func() *flagdef.InternalFlag
		// userKey is the targeting key of the evaluation, empty makes the bucketing fail.
		userKey     string
		name        string
		wantReason  flagdef.ResolutionReason
		nestedOwned bool // true when the nested maps of the metadata are copies too
	}{
		{
			name:    "disabled flag",
			userKey: ownedUserKey,
			flag: func() *flagdef.InternalFlag {
				return &flagdef.InternalFlag{
					Variations:  variations(),
					DefaultRule: &flagdef.Rule{VariationResult: new("on")},
					Disable:     new(true),
					Metadata:    new(ownedMetadata()),
				}
			},
			wantReason:  flagdef.ReasonDisabled,
			nestedOwned: true,
		},
		{
			name:    "bucketing key missing",
			userKey: "",
			flag: func() *flagdef.InternalFlag {
				return &flagdef.InternalFlag{
					Variations: variations(),
					DefaultRule: &flagdef.Rule{
						Percentages: &map[string]float64{"on": 50, "off": 50},
					},
					Metadata: new(ownedMetadata()),
				}
			},
			wantReason:  flagdef.ReasonError,
			nestedOwned: true,
		},
		{
			name:    "invalid targeting rule",
			userKey: ownedUserKey,
			flag: func() *flagdef.InternalFlag {
				return &flagdef.InternalFlag{
					Variations: variations(),
					Rules: &[]flagdef.Rule{
						{Query: new(`key eq "` + ownedUserKey + `"`), Percentages: &map[string]float64{}},
					},
					DefaultRule: &flagdef.Rule{VariationResult: new("on")},
					Metadata:    new(ownedMetadata()),
				}
			},
			wantReason:  flagdef.ReasonError,
			nestedOwned: true,
		},
		{
			name:    "enabled flag",
			userKey: ownedUserKey,
			flag: func() *flagdef.InternalFlag {
				return &flagdef.InternalFlag{
					Variations:  variations(),
					DefaultRule: &flagdef.Rule{VariationResult: new("on")},
					Metadata:    new(ownedMetadata()),
				}
			},
			wantReason: flagdef.ReasonStatic,
		},
		{
			name:    "enabled flag with a named rule",
			userKey: ownedUserKey,
			flag: func() *flagdef.InternalFlag {
				return &flagdef.InternalFlag{
					Variations: variations(),
					Rules: &[]flagdef.Rule{
						{Name: new("rule-1"), Query: new(`key eq "` + ownedUserKey + `"`), VariationResult: new("on")},
					},
					DefaultRule: &flagdef.Rule{VariationResult: new("off")},
					Metadata:    new(ownedMetadata()),
				}
			},
			wantReason: flagdef.ReasonTargetingMatch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flg := tt.flag()
			flagContext := flagdef.Context{Service: flagdef.NewService()}
			evalCtx := ffcontext.NewEvaluationContext(tt.userKey)

			_, first := flg.Value("flag", evalCtx, flagContext)
			require.Equal(t, tt.wantReason, first.Reason)
			require.NotEmpty(t, first.Metadata)

			// the caller changes what it received.
			first.Metadata["owner"] = "someone-else"
			first.Metadata["semaphore_cacheable"] = true
			delete(first.Metadata, "links")

			assert.Equal(t, ownedMetadata(), flg.GetMetadata(), "the metadata of the flag was changed")

			if tt.nestedOwned {
				_, again := flg.Value("flag", evalCtx, flagContext)
				asMap(t, again.Metadata["links"])["docs"] = "changed"

				assert.Equal(t, ownedMetadata(), flg.GetMetadata(), "a nested map of the flag was changed")
			}

			// evaluations that overlap, each changing its own result: the flag is only read.
			start := make(chan struct{})

			var wg sync.WaitGroup

			for range ownedRaceWorkers {
				wg.Go(func() {
					<-start

					for range ownedRaceRounds {
						_, res := flg.Value("flag", evalCtx, flagContext)
						res.Metadata["semaphore_cacheable"] = true
					}
				})
				wg.Go(func() {
					<-start

					for range ownedRaceRounds {
						assert.Equal(t, ownedMetadata(), flg.DeepCopy().GetMetadata())
					}
				})
			}

			close(start)
			wg.Wait()

			assert.Equal(t, ownedMetadata(), flg.GetMetadata())
		})
	}
}
