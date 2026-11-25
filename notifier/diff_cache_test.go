package notifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestDiffCache_HasDiff(t *testing.T) {
	t.Parallel()

	type fields struct {
		Deleted map[string]flag.Flag
		Added   map[string]flag.Flag
		Updated map[string]notifier.DiffUpdated
	}

	tests := []struct {
		fields fields
		name   string
		want   bool
	}{
		{
			name:   "null fields",
			fields: fields{},
			want:   false,
		},
		{
			name: "empty fields",
			fields: fields{
				Deleted: map[string]flag.Flag{},
				Added:   map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
			},
			want: false,
		},
		{
			name: "only Deleted",
			fields: fields{
				Deleted: map[string]flag.Flag{
					fixtures.FlagKeyBare: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleDefault),
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
			want: true,
		},
		{
			name: "only Added",
			fields: fields{
				Added: map[string]flag.Flag{
					fixtures.FlagKeyBare: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleDefault),
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
			want: true,
		},
		{
			name: "only Updated",
			fields: fields{
				Added:   map[string]flag.Flag{},
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyBare: {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(true)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(fixtures.RuleDefault),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(fixtures.RuleDefault),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
					},
				},
			},
			want: true,
		},
		{
			name: "all fields",
			fields: fields{
				Added: map[string]flag.Flag{
					fixtures.FlagKeyBare: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleDefault),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				Deleted: map[string]flag.Flag{
					fixtures.FlagKeyBare: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(fixtures.RuleDefault),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyBare: {
						Before: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(true)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(fixtures.RuleDefault),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(fixtures.RuleDefault),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := notifier.DiffCache{
				Deleted: tt.fields.Deleted,
				Added:   tt.fields.Added,
				Updated: tt.fields.Updated,
			}
			assert.Equal(t, tt.want, d.HasDiff())
		})
	}
}
