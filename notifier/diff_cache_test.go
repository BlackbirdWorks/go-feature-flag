package notifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestDiffCache_HasDiff(t *testing.T) {
	t.Parallel()

	type fields struct {
		Deleted map[string]flagdef.Flag
		Added   map[string]flagdef.Flag
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
				Deleted: map[string]flagdef.Flag{},
				Added:   map[string]flagdef.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
			},
			want: false,
		},
		{
			name: "only Deleted",
			fields: fields{
				Deleted: map[string]flagdef.Flag{
					fixtures.FlagKeyBare: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleDefault),
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
			want: true,
		},
		{
			name: "only Added",
			fields: fields{
				Added: map[string]flagdef.Flag{
					fixtures.FlagKeyBare: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleDefault),
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
			want: true,
		},
		{
			name: "only Updated",
			fields: fields{
				Added:   map[string]flagdef.Flag{},
				Deleted: map[string]flagdef.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyBare: {
						Before: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(true)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flagdef.Rule{
								Name: new(fixtures.RuleDefault),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						After: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flagdef.Rule{
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
				Added: map[string]flagdef.Flag{
					fixtures.FlagKeyBare: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
							Name: new(fixtures.RuleDefault),
							Percentages: &map[string]float64{
								fixtures.VariationFalse: 0,
								fixtures.VariationTrue:  100,
							},
						},
					},
				},
				Deleted: map[string]flagdef.Flag{
					fixtures.FlagKeyBare: &flagdef.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(true)),
							fixtures.VariationTrue:    new(any(true)),
							fixtures.VariationFalse:   new(any(true)),
						},
						DefaultRule: &flagdef.Rule{
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
						Before: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(true)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flagdef.Rule{
								Name: new(fixtures.RuleDefault),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						After: &flagdef.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
								fixtures.VariationFalse:   new(any(true)),
							},
							DefaultRule: &flagdef.Rule{
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
