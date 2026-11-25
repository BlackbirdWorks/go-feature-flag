package logsnotifier

import (
	"log/slog"
	"testing"

	"github.com/thejerf/slogassert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

type logNotifierCase struct {
	diff        notifier.DiffCache
	expectedLog *slogassert.LogMessageMatch
	name        string
}

func runLogNotifierTests(t *testing.T, tests []logNotifierCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := slogassert.New(t, slog.LevelDebug, nil)
			logger := slog.New(handler)
			c := &Notifier{
				Logger: logger,
			}
			_ = c.Notify(tt.diff)
			handler.AssertPrecise(*tt.expectedLog)
		})
	}
}

func TestLogNotifier_Notify(t *testing.T) {
	t.Parallel()

	runLogNotifierTests(t, []logNotifierCase{
		{
			name: "Flag deleted",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{
					fixtures.FlagKeyTest: &flag.InternalFlag{
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
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
				Updated: map[string]notifier.DiffUpdated{},
				Added:   map[string]flag.Flag{},
			},
			expectedLog: &slogassert.LogMessageMatch{
				Message: "flag removed",
				Level:   slog.LevelInfo,
				Attrs: map[string]any{
					keyField: fixtures.FlagKeyTest,
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Update flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyTest: {
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(fixtures.RuleOne),
									Query: new(fixtures.QueryKeyEqRandomKey),
									Percentages: &map[string]float64{
										fixtures.VariationFalse: 0,
										fixtures.VariationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(fixtures.RuleDefault),
								VariationResult: new(fixtures.VariationDefault),
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
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
				Added: map[string]flag.Flag{},
			},
			expectedLog: &slogassert.LogMessageMatch{
				Message: "flag updated",
				Level:   slog.LevelInfo,
				Attrs: map[string]any{
					keyField: fixtures.FlagKeyTest,
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Disable flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyTest: {
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(fixtures.RuleOne),
									Query: new(fixtures.QueryKeyEqRandomKey),
									Percentages: &map[string]float64{
										fixtures.VariationFalse: 0,
										fixtures.VariationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(fixtures.RuleDefault),
								VariationResult: new(fixtures.VariationDefault),
							},
						},
						After: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(fixtures.RuleOne),
									Query: new(fixtures.QueryKeyEqRandomKey),
									Percentages: &map[string]float64{
										fixtures.VariationFalse: 0,
										fixtures.VariationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(fixtures.RuleDefault),
								VariationResult: new(fixtures.VariationDefault),
							},
							Disable: new(true),
						},
					},
				},
				Added: map[string]flag.Flag{},
			},
			expectedLog: &slogassert.LogMessageMatch{
				Message: "flag is turned OFF",
				Level:   slog.LevelInfo,
				Attrs: map[string]any{
					keyField: fixtures.FlagKeyTest,
				},
				AllAttrsMatch: true,
			},
		},
	})
}

func TestLogNotifier_Notify_AddAndToggle(t *testing.T) {
	t.Parallel()

	runLogNotifierTests(t, []logNotifierCase{
		{
			name: "Add flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
				Added: map[string]flag.Flag{
					"add-test-flag": &flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new(fixtures.RuleOne),
								Query: new(fixtures.QueryKeyEqRandomKey),
								Percentages: &map[string]float64{
									fixtures.VariationFalse: 0,
									fixtures.VariationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							fixtures.VariationDefault: new(any(false)),
							fixtures.VariationFalse:   new(any(false)),
							fixtures.VariationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(fixtures.RuleDefault),
							VariationResult: new(fixtures.VariationDefault),
						},
					},
				},
			},
			expectedLog: &slogassert.LogMessageMatch{
				Message: "flag added",
				Level:   slog.LevelInfo,
				Attrs: map[string]any{
					keyField: "add-test-flag",
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Enable flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					fixtures.FlagKeyTest: {
						After: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(fixtures.RuleOne),
									Query: new(fixtures.QueryKeyEqRandomKey),
									Percentages: &map[string]float64{
										fixtures.VariationFalse: 0,
										fixtures.VariationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(fixtures.RuleDefault),
								VariationResult: new(fixtures.VariationDefault),
							},
						},
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(fixtures.RuleOne),
									Query: new(fixtures.QueryKeyEqRandomKey),
									Percentages: &map[string]float64{
										fixtures.VariationFalse: 0,
										fixtures.VariationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								fixtures.VariationDefault: new(any(false)),
								fixtures.VariationFalse:   new(any(false)),
								fixtures.VariationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(fixtures.RuleDefault),
								VariationResult: new(fixtures.VariationDefault),
							},
							Disable: new(true),
						},
					},
				},
				Added: map[string]flag.Flag{},
			},
			expectedLog: &slogassert.LogMessageMatch{
				Message: "flag is turned ON",
				Level:   slog.LevelInfo,
				Attrs: map[string]any{
					keyField: fixtures.FlagKeyTest,
				},
				AllAttrsMatch: true,
			},
		},
	})
}
