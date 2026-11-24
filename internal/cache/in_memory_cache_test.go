package cache_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/model/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

func TestAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		param map[string]dto.DTO
		want  map[string]flag.Flag
	}{
		{
			name: "all with 1 flag",
			param: map[string]dto.DTO{
				"test": {
					Variations: &map[string]*any{
						"True":    new(any("true")),
						"False":   new(any("false")),
						"Default": new(any("default")),
					},
					DefaultRule: &flag.Rule{
						Percentages: &map[string]float64{
							"True":  40,
							"False": 60,
						},
					},
				},
			},
			want: map[string]flag.Flag{
				"test": &flag.InternalFlag{
					Variations: &map[string]*any{
						"Default": new(any("default")),
						"False":   new(any("false")),
						"True":    new(any("true")),
					},
					DefaultRule: &flag.Rule{
						Percentages: &map[string]float64{
							"False": 60,
							"True":  40,
						},
					},
				},
			},
		},
		{
			name: "all with multiple flags",
			param: map[string]dto.DTO{
				"test": {
					Variations: &map[string]*any{
						"True":    new(any("true")),
						"False":   new(any("false")),
						"Default": new(any("default")),
					},
					DefaultRule: &flag.Rule{
						Name: new("defaultRule"),
						Percentages: &map[string]float64{
							"True":  40,
							"False": 60,
						},
					},
				},
				"test1": {
					Variations: &map[string]*any{
						"True":    new(any(true)),
						"False":   new(any(false)),
						"Default": new(any(false)),
					},
					DefaultRule: &flag.Rule{
						Name: new("defaultRule"),
						Percentages: &map[string]float64{
							"True":  30,
							"False": 70,
						},
					},
				},
			},
			want: map[string]flag.Flag{
				"test": &flag.InternalFlag{
					Variations: &map[string]*any{
						"Default": new(any("default")),
						"False":   new(any("false")),
						"True":    new(any("true")),
					},
					DefaultRule: &flag.Rule{
						Name: new("defaultRule"),
						Percentages: &map[string]float64{
							"False": 60,
							"True":  40,
						},
					},
				},
				"test1": &flag.InternalFlag{
					Variations: &map[string]*any{
						"Default": new(any(false)),
						"False":   new(any(false)),
						"True":    new(any(true)),
					},
					DefaultRule: &flag.Rule{
						Name: new("defaultRule"),
						Percentages: &map[string]float64{
							"False": 70,
							"True":  30,
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
		name  string
		param map[string]dto.DTO
	}{
		{
			name: "copy with 1 flag",
			param: map[string]dto.DTO{
				"test": {
					Variations: &map[string]*any{
						"True":    new(any("true")),
						"False":   new(any("false")),
						"Default": new(any("default")),
					},
					DefaultRule: &flag.Rule{
						Name: new("defaultRule"),
						Percentages: &map[string]float64{
							"True":  40,
							"False": 60,
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
			assert.Equal(t, c, got)
		})
	}
}
