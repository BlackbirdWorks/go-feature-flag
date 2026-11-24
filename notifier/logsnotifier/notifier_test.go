package logsnotifier

import (
	"log/slog"
	"testing"

	"github.com/thejerf/slogassert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

func TestLogNotifier_Notify(t *testing.T) {
	t.Parallel()

	type args struct {
	}
	tests := []struct {
		name        string
		args        args
		diff        notifier.DiffCache
		expectedLog *slogassert.LogMessageMatch
	}{
		{
			name: "Flag deleted",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{
					"test-flag": &flag.InternalFlag{
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new("defaultRule"),
							Percentages: &map[string]float64{
								"False": 0,
								"True":  100,
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
					"key": "test-flag",
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Update flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					"test-flag": {
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new("rule1"),
									Query: new("key eq \"random-key\""),
									Percentages: &map[string]float64{
										"False": 0,
										"True":  100,
									},
								},
							},
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new("defaultRule"),
								VariationResult: new("Default"),
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new("defaultRule"),
								Percentages: &map[string]float64{
									"False": 0,
									"True":  100,
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
					"key": "test-flag",
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Disable flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					"test-flag": {
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new("rule1"),
									Query: new("key eq \"random-key\""),
									Percentages: &map[string]float64{
										"False": 0,
										"True":  100,
									},
								},
							},
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new("defaultRule"),
								VariationResult: new("Default"),
							},
						},
						After: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new("rule1"),
									Query: new("key eq \"random-key\""),
									Percentages: &map[string]float64{
										"False": 0,
										"True":  100,
									},
								},
							},
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new("defaultRule"),
								VariationResult: new("Default"),
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
					"key": "test-flag",
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Add flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{},
				Added: map[string]flag.Flag{
					"add-test-flag": &flag.InternalFlag{
						Rules: &[]flag.Rule{
							{
								Name:  new("rule1"),
								Query: new("key eq \"random-key\""),
								Percentages: &map[string]float64{
									"False": 0,
									"True":  100,
								},
							},
						},
						Variations: &map[string]*any{
							"Default": new(any(false)),
							"False":   new(any(false)),
							"True":    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new("defaultRule"),
							VariationResult: new("Default"),
						},
					},
				},
			},
			expectedLog: &slogassert.LogMessageMatch{
				Message: "flag added",
				Level:   slog.LevelInfo,
				Attrs: map[string]any{
					"key": "add-test-flag",
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Enable flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					"test-flag": {
						After: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new("rule1"),
									Query: new("key eq \"random-key\""),
									Percentages: &map[string]float64{
										"False": 0,
										"True":  100,
									},
								},
							},
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new("defaultRule"),
								VariationResult: new("Default"),
							},
						},
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new("rule1"),
									Query: new("key eq \"random-key\""),
									Percentages: &map[string]float64{
										"False": 0,
										"True":  100,
									},
								},
							},
							Variations: &map[string]*any{
								"Default": new(any(false)),
								"False":   new(any(false)),
								"True":    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new("defaultRule"),
								VariationResult: new("Default"),
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
					"key": "test-flag",
				},
				AllAttrsMatch: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := slogassert.New(t, slog.LevelDebug, nil)
			logger := slog.New(handler)
			c := &Notifier{
				Logger: &fflog.FFLogger{LeveledLogger: logger},
			}
			_ = c.Notify(tt.diff)
			handler.AssertPrecise(*tt.expectedLog)
		})
	}
}
