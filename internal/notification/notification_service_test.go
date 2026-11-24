package notification

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

func Test_notificationService_getDifferences(t *testing.T) {
	t.Parallel()

	type fields struct {
		Notifiers []notifier.Notifier
	}
	type args struct {
		oldCache map[string]flag.Flag
		newCache map[string]flag.Flag
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   notifier.DiffCache
	}{
		{
			name: "Delete flag",
			args: args{
				oldCache: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
					"test-flag2": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Deleted: map[string]flag.Flag{
					"test-flag2": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
				Added:   map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
			},
		},
		{
			name: "Added flag",
			args: args{
				oldCache: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
					"test-flag2": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Added: map[string]flag.Flag{
					"test-flag2": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
			},
		},
		{
			name: "Updated flag",
			args: args{
				oldCache: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(true)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("rule1"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Added:   map[string]flag.Flag{},
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					"test-flag": {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new("rule1"),
								Percentages: &map[string]float64{
									"False": 0,
									"True":  100,
								},
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								"Default": new(any(true)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new("rule1"),
								Percentages: &map[string]float64{
									"False": 0,
									"True":  100,
								},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &notificationService{
				Notifiers: tt.fields.Notifiers,
				waitGroup: &sync.WaitGroup{},
			}
			got := c.getDifferences(tt.args.oldCache, tt.args.newCache)
			assert.Equal(t, tt.want, got)
		})
	}
}
