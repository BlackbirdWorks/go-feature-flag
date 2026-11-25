package notification

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	variationDefault = "Default"
	variationFalse   = "False"
	variationTrue    = "True"
	rule1            = "rule1"
	testFlag         = "test-flag"
	testFlag2        = "test-flag2"
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
					testFlag: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					testFlag2: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					testFlag: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Deleted: map[string]flag.Flag{
					testFlag2: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
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
					testFlag: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					testFlag: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
					testFlag2: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Added: map[string]flag.Flag{
					testFlag2: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
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
					testFlag: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
				},
				newCache: map[string]flag.Flag{
					testFlag: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(true)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(rule1),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
							},
						},
					},
				},
			},
			want: notifier.DiffCache{
				Added:   map[string]flag.Flag{},
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					testFlag: {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								variationDefault: new(any(false)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(rule1),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								variationDefault: new(any(true)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(rule1),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
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
