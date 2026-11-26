package flagdef_test

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/ffcontext"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
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
		userKey      string
		name         string
		wantReason   flagdef.ResolutionReason
		wantRuleName string
		// wantNestedOwned: the nested maps of the metadata are copies (true) or shared and read-only (false).
		wantNestedOwned bool
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
			wantReason:      flagdef.ReasonDisabled,
			wantNestedOwned: true,
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
			wantReason:      flagdef.ReasonError,
			wantNestedOwned: true,
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
			wantReason:      flagdef.ReasonError,
			wantNestedOwned: true,
		},
		{
			name:    "experiment over",
			userKey: ownedUserKey,
			flag: func() *flagdef.InternalFlag {
				return &flagdef.InternalFlag{
					Variations:      variations(),
					DefaultRule:     &flagdef.Rule{VariationResult: new("on")},
					Experimentation: &flagdef.ExperimentationRollout{End: new(clocktest.Instant().Add(-time.Hour))},
					Metadata:        new(ownedMetadata()),
				}
			},
			wantReason:      flagdef.ReasonDisabled,
			wantNestedOwned: true,
		},
		{
			name:    "memoized scheduled flag",
			userKey: ownedUserKey,
			flag: func() *flagdef.InternalFlag {
				return &flagdef.InternalFlag{
					Variations:  variations(),
					DefaultRule: &flagdef.Rule{VariationResult: new("off")},
					Scheduled: &[]flagdef.ScheduledStep{
						{
							Date:        new(clocktest.Instant().Add(-time.Hour)),
							DefaultRule: &flagdef.Rule{VariationResult: new("on")},
						},
					},
					Metadata: new(ownedMetadata()),
				}
			},
			wantReason:      flagdef.ReasonStatic,
			wantNestedOwned: true,
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
			wantReason:   flagdef.ReasonTargetingMatch,
			wantRuleName: "rule-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flg := tt.flag()
			flagContext := flagdef.Context{Service: flagdef.NewService()}
			evalCtx := ffcontext.NewEvaluationContext(tt.userKey)

			// the content every evaluation of this flag must return.
			want := ownedMetadata()
			if tt.wantRuleName != "" {
				want["evaluatedRuleName"] = tt.wantRuleName
			}

			_, first := flg.Value("flag", evalCtx, flagContext)
			require.Equal(t, tt.wantReason, first.Reason)
			require.Equal(t, want, first.Metadata)

			// the caller changes what it received: top-level keys, and the nested containers it owns.
			first.Metadata["owner"] = "someone-else"
			first.Metadata["semaphore_cacheable"] = true
			delete(first.Metadata, "links")

			if tt.wantNestedOwned {
				_, nested := flg.Value("flag", evalCtx, flagContext)
				asMap(t, nested.Metadata["links"])["docs"] = "changed"
			}

			assert.Equal(t, ownedMetadata(), flg.GetMetadata(), "the metadata of the flag was changed")

			// a second evaluation (of the memoized flag for a scheduled one) is not affected by those changes.
			_, second := flg.Value("flag", evalCtx, flagContext)
			assert.Equal(t, want, second.Metadata, "the second evaluation returns what the caller changed")

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

// a flag without metadata and an evaluation without a named rule have no metadata: the result is nil (the caller
// creates a map before adding keys).
func TestInternalFlag_Value_NoMetadataIsNil(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag          *flagdef.InternalFlag
		wantReason    flagdef.ResolutionReason
		wantErrorCode flagdef.ErrorCode
		name          string
	}{
		{
			name:       "enabled",
			wantReason: flagdef.ReasonStatic,
			flag: &flagdef.InternalFlag{
				Variations:  &map[string]*any{"on": new(any("on"))},
				DefaultRule: &flagdef.Rule{VariationResult: new("on")},
			},
		},
		{
			name:       "disabled",
			wantReason: flagdef.ReasonDisabled,
			flag: &flagdef.InternalFlag{
				Variations:  &map[string]*any{"on": new(any("on"))},
				DefaultRule: &flagdef.Rule{VariationResult: new("on")},
				Disable:     new(true),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, details := tt.flag.Value("flag", ffcontext.NewEvaluationContext(ownedUserKey),
				flagdef.Context{Service: flagdef.NewService()})
			assert.Equal(t, tt.wantReason, details.Reason)
			assert.Equal(t, tt.wantErrorCode, details.ErrorCode)
			assert.Nil(t, details.Metadata)
		})
	}
}
