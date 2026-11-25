package cache_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const (
	defaultRule2     = "defaultRule"
	variationDefault = "Default"
	lowerDefault     = "default"
	variationFalse   = "False"
	variationTrue    = "True"
	test2            = "test"
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
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
						variationDefault: new(any(lowerDefault)),
					},
					DefaultRule: &flag.Rule{
						Percentages: &map[string]float64{
							variationTrue:  40,
							variationFalse: 60,
						},
					},
				},
			},
			want: map[string]flag.Flag{
				test2: &flag.InternalFlag{
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationFalse:   new(any(lowerFalse)),
						variationTrue:    new(any(lowerTrue)),
					},
					DefaultRule: &flag.Rule{
						Percentages: &map[string]float64{
							variationFalse: 60,
							variationTrue:  40,
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
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
						variationDefault: new(any(lowerDefault)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							variationTrue:  40,
							variationFalse: 60,
						},
					},
				},
				"test1": {
					Variations: &map[string]*any{
						variationTrue:    new(any(true)),
						variationFalse:   new(any(false)),
						variationDefault: new(any(false)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							variationTrue:  30,
							variationFalse: 70,
						},
					},
				},
			},
			want: map[string]flag.Flag{
				test2: &flag.InternalFlag{
					Variations: &map[string]*any{
						variationDefault: new(any(lowerDefault)),
						variationFalse:   new(any(lowerFalse)),
						variationTrue:    new(any(lowerTrue)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							variationFalse: 60,
							variationTrue:  40,
						},
					},
				},
				"test1": &flag.InternalFlag{
					Variations: &map[string]*any{
						variationDefault: new(any(false)),
						variationFalse:   new(any(false)),
						variationTrue:    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							variationFalse: 70,
							variationTrue:  30,
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
						variationTrue:    new(any(lowerTrue)),
						variationFalse:   new(any(lowerFalse)),
						variationDefault: new(any(lowerDefault)),
					},
					DefaultRule: &flag.Rule{
						Name: new(defaultRule2),
						Percentages: &map[string]float64{
							variationTrue:  40,
							variationFalse: 60,
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
