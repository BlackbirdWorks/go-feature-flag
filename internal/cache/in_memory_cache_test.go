package cache_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		param map[string]dto.DTO
		want  map[string]flagdef.Flag
		name  string
	}{
		{
			name: "all with 1 flag",
			param: map[string]dto.DTO{
				fixtures.PlaceholderTest: {
					Variations: &map[string]*any{
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
					},
					DefaultRule: &flagdef.Rule{
						Percentages: &map[string]float64{
							fixtures.VariationTrue:  40,
							fixtures.VariationFalse: 60,
						},
					},
				},
			},
			want: map[string]flagdef.Flag{
				fixtures.PlaceholderTest: &flagdef.InternalFlag{
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
					},
					DefaultRule: &flagdef.Rule{
						Percentages: &map[string]float64{
							fixtures.VariationFalse: 60,
							fixtures.VariationTrue:  40,
						},
					},
				},
			},
		},
		{
			name: "all with multiple flags",
			param: map[string]dto.DTO{
				fixtures.PlaceholderTest: {
					Variations: &map[string]*any{
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
					},
					DefaultRule: &flagdef.Rule{
						Name: new(fixtures.RuleDefault),
						Percentages: &map[string]float64{
							fixtures.VariationTrue:  40,
							fixtures.VariationFalse: 60,
						},
					},
				},
				"test1": {
					Variations: &map[string]*any{
						fixtures.VariationTrue:    new(any(true)),
						fixtures.VariationFalse:   new(any(false)),
						fixtures.VariationDefault: new(any(false)),
					},
					DefaultRule: &flagdef.Rule{
						Name: new(fixtures.RuleDefault),
						Percentages: &map[string]float64{
							fixtures.VariationTrue:  30,
							fixtures.VariationFalse: 70,
						},
					},
				},
			},
			want: map[string]flagdef.Flag{
				fixtures.PlaceholderTest: &flagdef.InternalFlag{
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
					},
					DefaultRule: &flagdef.Rule{
						Name: new(fixtures.RuleDefault),
						Percentages: &map[string]float64{
							fixtures.VariationFalse: 60,
							fixtures.VariationTrue:  40,
						},
					},
				},
				"test1": &flagdef.InternalFlag{
					Variations: &map[string]*any{
						fixtures.VariationDefault: new(any(false)),
						fixtures.VariationFalse:   new(any(false)),
						fixtures.VariationTrue:    new(any(true)),
					},
					DefaultRule: &flagdef.Rule{
						Name: new(fixtures.RuleDefault),
						Percentages: &map[string]float64{
							fixtures.VariationFalse: 70,
							fixtures.VariationTrue:  30,
						},
					},
				},
			},
		},
		{
			name:  "empty",
			param: map[string]dto.DTO{},
			want:  map[string]flagdef.Flag{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewInMemoryCache(flagdef.NewService(), nil)
			c.Init(tt.param)
			assert.Equal(t, tt.want, c.All())
		})
	}
}

func TestCopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		param map[string]dto.DTO
		name  string
		// wantEqual is whether the copy equals the cache it was taken from
		wantEqual bool
	}{
		{
			name:      "copy with 1 flag",
			wantEqual: true,
			param: map[string]dto.DTO{
				fixtures.PlaceholderTest: {
					Variations: &map[string]*any{
						fixtures.VariationTrue:    new(any(fixtures.TrueLiteral)),
						fixtures.VariationFalse:   new(any(fixtures.FalseLiteral)),
						fixtures.VariationDefault: new(any(fixtures.FlagsetDefault)),
					},
					DefaultRule: &flagdef.Rule{
						Name: new(fixtures.RuleDefault),
						Percentages: &map[string]float64{
							fixtures.VariationTrue:  40,
							fixtures.VariationFalse: 60,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewInMemoryCache(flagdef.NewService(), nil)
			c.Init(tt.param)
			got := c.Copy()
			assert.Equal(t, tt.wantEqual, assert.ObjectsAreEqual(c, got))
		})
	}
}
