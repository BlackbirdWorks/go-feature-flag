package notifier_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

func TestFlagChanges(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	baseFlag := func() *flagdef.InternalFlag {
		return &flagdef.InternalFlag{
			Variations: &map[string]*any{
				"on":                  new(any(true)),
				fixtures.VariationOff: new(any(false)),
			},
			DefaultRule: &flagdef.Rule{VariationResult: new(fixtures.VariationOff)},
			Metadata:    &map[string]any{"team": payments},
		}
	}

	tests := []struct {
		name   string
		update func(f *flagdef.InternalFlag)
		want   []notifier.FieldChange
	}{
		{
			name:   "should show a disabled flag without pointer syntax",
			update: func(f *flagdef.InternalFlag) { f.Disable = new(true) },
			want:   []notifier.FieldChange{{Path: disable, From: fixtures.NullLiteral, To: "true"}},
		},
		{
			name: "should show a new rule as JSON",
			update: func(f *flagdef.InternalFlag) {
				f.Rules = &[]flagdef.Rule{{
					Name:            new(beta),
					Query:           new(`country eq "FR"`),
					VariationResult: new("on"),
				}}
			},
			want: []notifier.FieldChange{{
				Path: "Rules",
				From: fixtures.NullLiteral,
				To:   `[{"name":"beta","query":"country eq \"FR\"","variation":"on"}]`,
			}},
		},
		{
			name: "should show a percentage rollout sorted by path",
			update: func(f *flagdef.InternalFlag) {
				f.DefaultRule = &flagdef.Rule{Percentages: &map[string]float64{"on": 10, fixtures.VariationOff: 90}}
			},
			want: []notifier.FieldChange{
				{Path: "DefaultRule.Percentages", From: fixtures.NullLiteral, To: `{"off":90,"on":10}`},
				{Path: "DefaultRule.VariationResult", From: `"off"`, To: fixtures.NullLiteral},
			},
		},
		{
			name: "should show dates as RFC 3339",
			update: func(f *flagdef.InternalFlag) {
				f.Experimentation = &flagdef.ExperimentationRollout{Start: &start, End: &end}
			},
			want: []notifier.FieldChange{{
				Path: "Experimentation",
				From: fixtures.NullLiteral,
				To:   `{"start":"2026-10-05T09:00:00Z","end":"2026-10-12T09:00:00Z"}`,
			}},
		},
		{
			name: "should report a metadata key that was added",
			update: func(f *flagdef.InternalFlag) {
				f.Metadata = &map[string]any{"team": payments, owner: fixtures.UserJane}
			},
			want: []notifier.FieldChange{{Path: "Metadata.owner", From: fixtures.NullLiteral, To: `"jane"`}},
		},
		{
			name:   "should report a metadata key that was removed",
			update: func(f *flagdef.InternalFlag) { f.Metadata = &map[string]any{} },
			want:   []notifier.FieldChange{{Path: "Metadata.team", From: `"payments"`, To: fixtures.NullLiteral}},
		},
		{
			name: "should report a variation that was added",
			update: func(f *flagdef.InternalFlag) {
				(*f.Variations)[beta] = new(any("blue"))
			},
			want: []notifier.FieldChange{{Path: "Variations.beta", From: fixtures.NullLiteral, To: `"blue"`}},
		},
		{
			name:   "should return nothing when the flag did not change",
			update: func(_ *flagdef.InternalFlag) {},
			want:   []notifier.FieldChange{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			after := baseFlag()
			tt.update(after)
			got, err := notifier.FlagChanges(baseFlag(), after)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFlagChanges_error(t *testing.T) {
	t.Parallel()

	before := &flagdef.InternalFlag{Metadata: &map[string]any{"hook": func() {}}}
	after := &flagdef.InternalFlag{Metadata: &map[string]any{"hook": func() {}}}
	got, err := notifier.FlagChanges(before, after)
	require.ErrorContains(t, err, "impossible to compute flag changes")
	assert.Nil(t, got)
}

func TestReadableValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "nil", value: nil, want: fixtures.NullLiteral},
		{name: "string pointer", value: new("on"), want: `"on"`},
		{name: "map", value: map[string]float64{"on": 50, fixtures.VariationOff: 50}, want: `{"off":50,"on":50}`},
		{name: "keeps HTML characters as typed", value: "a < b && c", want: `"a < b && c"`},
		{name: "falls back to Go syntax when JSON cannot encode the value", value: make(chan int), want: "(chan int)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Contains(t, notifier.ReadableValue(tt.value), tt.want)
		})
	}
}
