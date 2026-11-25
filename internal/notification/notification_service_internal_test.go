package notification

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
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
		want   notifier.DiffCache
		args   args
		name   string
		fields fields
	}{
		{
			name: "Delete flag",
			args: args{
				oldCache: map[string]flag.Flag{
					fixtures.FlagKeyTest: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					fixtures.FlagKeyTestSecond: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					fixtures.FlagKeyTest: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Deleted: map[string]flag.Flag{
					fixtures.FlagKeyTestSecond: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
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
					fixtures.FlagKeyTest: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					fixtures.FlagKeyTest: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					fixtures.FlagKeyTestSecond: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Added: map[string]flag.Flag{
					fixtures.FlagKeyTestSecond: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
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
					fixtures.FlagKeyTest: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					fixtures.FlagKeyTest: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Added:   map[string]flag.Flag{},
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyTest: {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(fixtures.RuleOne),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(true)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(fixtures.RuleOne),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
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

			c := &ServiceImpl{
				Notifiers: tt.fields.Notifiers,
				waitGroup: &sync.WaitGroup{},
			}
			got := c.getDifferences(tt.args.oldCache, tt.args.newCache)
			assert.Equal(t, tt.want, got)
		})
	}
}
