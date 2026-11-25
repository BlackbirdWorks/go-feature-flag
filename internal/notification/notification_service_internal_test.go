package notification

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func Test_notificationService_getDifferences(t *testing.T) {
	t.Parallel()

	type fields struct {
		Notifiers []notifier.Notifier
	}

	type args struct {
		oldCache map[string]flagdef.Flag
		newCache map[string]flagdef.Flag
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
				oldCache: map[string]flagdef.Flag{
					fixtures.FlagKeyTest: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					fixtures.FlagKeyTestSecond: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flagdef.Flag{
					fixtures.FlagKeyTest: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
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
				Deleted: map[string]flagdef.Flag{
					fixtures.FlagKeyTestSecond: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				Added:   map[string]flagdef.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
			},
		},
		{
			name: "Added flag",
			args: args{
				oldCache: map[string]flagdef.Flag{
					fixtures.FlagKeyTest: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flagdef.Flag{
					fixtures.FlagKeyTest: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
					fixtures.FlagKeyTestSecond: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
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
				Added: map[string]flagdef.Flag{
					fixtures.FlagKeyTestSecond: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				Deleted: map[string]flagdef.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
			},
		},
		{
			name: "Updated flag",
			args: args{
				oldCache: map[string]flagdef.Flag{
					fixtures.FlagKeyTest: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleOne),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flagdef.Flag{
					fixtures.FlagKeyTest: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
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
				Added:   map[string]flagdef.Flag{},
				Deleted: map[string]flagdef.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyTest: {
						Before: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flagdef.Rule{
								Name: new(fixtures.RuleOne),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						After: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(true)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flagdef.Rule{
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
