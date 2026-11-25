package logsnotifier

import (
	"log/slog"
	"testing"

	"github.com/thejerf/slogassert"

	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

const (
	defaultRule      = "defaultRule"
	variationDefault = "Default"
	variationFalse   = "False"
	variationTrue    = "True"
	keyEqRandomKey   = "key eq \"random-key\""
	rule1            = "rule1"
	testFlag         = "test-flag"
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
				Logger: &fflog.FFLogger{LeveledLogger: logger},
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
					testFlag: &flag.InternalFlag{
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name: new(defaultRule),
							Percentages: &map[string]float64{
								variationFalse: 0,
								variationTrue:  100,
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
					key2: testFlag,
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Update flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					testFlag: {
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(rule1),
									Query: new(keyEqRandomKey),
									Percentages: &map[string]float64{
										variationFalse: 0,
										variationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								variationDefault: new(any(false)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(defaultRule),
								VariationResult: new(variationDefault),
							},
						},
						After: &flag.InternalFlag{
							Variations: &map[string]*any{
								variationDefault: new(any(false)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name: new(defaultRule),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
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
					key2: testFlag,
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Disable flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					testFlag: {
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(rule1),
									Query: new(keyEqRandomKey),
									Percentages: &map[string]float64{
										variationFalse: 0,
										variationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								variationDefault: new(any(false)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(defaultRule),
								VariationResult: new(variationDefault),
							},
						},
						After: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(rule1),
									Query: new(keyEqRandomKey),
									Percentages: &map[string]float64{
										variationFalse: 0,
										variationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								variationDefault: new(any(false)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(defaultRule),
								VariationResult: new(variationDefault),
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
					key2: testFlag,
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
								Name:  new(rule1),
								Query: new(keyEqRandomKey),
								Percentages: &map[string]float64{
									variationFalse: 0,
									variationTrue:  100,
								},
							},
						},
						Variations: &map[string]*any{
							variationDefault: new(any(false)),
							variationFalse:   new(any(false)),
							variationTrue:    new(any(true)),
						},
						DefaultRule: &flag.Rule{
							Name:            new(defaultRule),
							VariationResult: new(variationDefault),
						},
					},
				},
			},
			expectedLog: &slogassert.LogMessageMatch{
				Message: "flag added",
				Level:   slog.LevelInfo,
				Attrs: map[string]any{
					key2: "add-test-flag",
				},
				AllAttrsMatch: true,
			},
		},
		{
			name: "Enable flag",
			diff: notifier.DiffCache{
				Deleted: map[string]flag.Flag{},
				Updated: map[string]notifier.DiffUpdated{
					testFlag: {
						After: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(rule1),
									Query: new(keyEqRandomKey),
									Percentages: &map[string]float64{
										variationFalse: 0,
										variationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								variationDefault: new(any(false)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(defaultRule),
								VariationResult: new(variationDefault),
							},
						},
						Before: &flag.InternalFlag{
							Rules: &[]flag.Rule{
								{
									Name:  new(rule1),
									Query: new(keyEqRandomKey),
									Percentages: &map[string]float64{
										variationFalse: 0,
										variationTrue:  100,
									},
								},
							},
							Variations: &map[string]*any{
								variationDefault: new(any(false)),
								variationFalse:   new(any(false)),
								variationTrue:    new(any(true)),
							},
							DefaultRule: &flag.Rule{
								Name:            new(defaultRule),
								VariationResult: new(variationDefault),
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
					key2: testFlag,
				},
				AllAttrsMatch: true,
			},
		},
	})
}
