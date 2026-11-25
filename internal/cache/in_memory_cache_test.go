package cache_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const (
	defaultRule2    = "defaultRule"
	fixtureDefault  = "Default"
	fixtureDefault2 = "default"
	fixtureFalse    = "False"
	fixtureTrue     = "True"
	test2           = "test"
)

func TestAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		param map[string]dto.DTO
		want  map[string]flag.Flag
		name  string
	}{
		{
			name: "all with 1 flag",
			param: map[string]dto.DTO{
				test2: {
					Variations: &map[string]*any{
						fixtureTrue:    new(any(fixtureTrue2)),
						fixtureFalse:   new(any(fixtureFalse2)),
						fixtureDefault: new(any(fixtureDefault2)),
					},
					DefaultRule: &flag.Rule{
						Percentages: &map[string]float64{
							fixtureTrue:  40,
							fixtureFalse: 60,
						},
					},
				},
			},
			want: map[string]flag.Flag{
				test2: &flag.InternalFlag{
					Variations: &map[string]*any{
						fixtureDefault: new(any(fixtureDefault2)),
						fixtureFalse:   new(any(fixtureFalse2)),
						fixtureTrue:    new(any(fixtureTrue2)),
					},
					DefaultRule: &flag.Rule{
						Percentages: &map[string]float64{
							fixtureFalse: 60,
							fixtureTrue:  40,
						},
					},
				},
			},
		},
		{
			name: "all with multiple flags",
			param: map[string]dto.DTO{
				test2: {
					Variations: &map[string]*any{
						fixtureTrue:    new(any(fixtureTrue2)),
						fixtureFalse:   new(any(fixtureFalse2)),
						fixtureDefault: new(any(fixtureDefault2)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							fixtureTrue:  40,
							fixtureFalse: 60,
						},
					},
				},
				"test1": {
					Variations: &map[string]*any{
						fixtureTrue:    new(any(true)),
						fixtureFalse:   new(any(false)),
						fixtureDefault: new(any(false)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							fixtureTrue:  30,
							fixtureFalse: 70,
						},
					},
				},
			},
			want: map[string]flag.Flag{
				test2: &flag.InternalFlag{
					Variations: &map[string]*any{
						fixtureDefault: new(any(fixtureDefault2)),
						fixtureFalse:   new(any(fixtureFalse2)),
						fixtureTrue:    new(any(fixtureTrue2)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							fixtureFalse: 60,
							fixtureTrue:  40,
						},
					},
				},
				"test1": &flag.InternalFlag{
					Variations: &map[string]*any{
						fixtureDefault: new(any(false)),
						fixtureFalse:   new(any(false)),
						fixtureTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							fixtureFalse: 70,
							fixtureTrue:  30,
						},
					},
				},
			},
		},
		{
			name:  "empty",
			param: map[string]dto.DTO{},
			want:  map[string]flag.Flag{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewInMemoryCache(flag.NewService(), nil)
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
				test2: {
					Variations: &map[string]*any{
						fixtureTrue:    new(any(fixtureTrue2)),
						fixtureFalse:   new(any(fixtureFalse2)),
						fixtureDefault: new(any(fixtureDefault2)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							fixtureTrue:  40,
							fixtureFalse: 60,
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := cache.NewInMemoryCache(flag.NewService(), nil)
			c.Init(tt.param)
			got := c.Copy()
			assert.Equal(t, tt.wantEqual, assert.ObjectsAreEqual(c, got))
		})
	}
}
