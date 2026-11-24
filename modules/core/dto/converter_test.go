package dto_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

func TestConvertV1DtoToInternalFlag(t *testing.T) {
	tests := []struct {
		name     string
		input    dto.DTO
		expected flag.InternalFlag
	}{
		{
			name: "all fields set",
			input: dto.DTO{
				BucketingKey: new("bucketKey"),
				Variations: &map[string]*any{
					"var1": new(any("var1")),
					"var2": new(any("var2")),
				},
				Rules: &[]flag.Rule{{
					Name:        new("rule1"),
					Query:       new("key eq \"key\""),
					Percentages: &map[string]float64{"var_true": 100, "var_false": 0}},
					{
						Name:  new("rule2"),
						Query: new("key eq \"key2\""),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new("var1"),
								Percentage: new(float64(30)),
								Date: new(
									time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new("var2"),
								Percentage: new(float64(70)),
								Date: new(
									time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
						},
					},
					{
						Name:            new("rule3"),
						Query:           new("key eq \"key3\""),
						VariationResult: new("var2"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("var1"),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new("var2"),
						},
					},
				},
				Experimentation: &dto.ExperimentationDto{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{"key": "value"},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
			},
			expected: flag.InternalFlag{
				BucketingKey: new("bucketKey"),
				Variations: &map[string]*any{
					"var1": new(any("var1")),
					"var2": new(any("var2")),
				},
				Rules: &[]flag.Rule{{
					Name:        new("rule1"),
					Query:       new("key eq \"key\""),
					Percentages: &map[string]float64{"var_true": 100, "var_false": 0}},
					{
						Name:  new("rule2"),
						Query: new("key eq \"key2\""),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new("var1"),
								Percentage: new(float64(30)),
								Date: new(
									time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new("var2"),
								Percentage: new(float64(70)),
								Date: new(
									time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
						},
					},
					{
						Name:            new("rule3"),
						Query:           new("key eq \"key3\""),
						VariationResult: new("var2"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("var1"),
				},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new("var2"),
						},
					},
				},
				Experimentation: &flag.ExperimentationRollout{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata: &map[string]any{"key": "value"},
			},
		},
		{
			name:     "nil input",
			input:    dto.DTO{},
			expected: flag.InternalFlag{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.input.Convert()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertInternalFlagToDto(t *testing.T) {
	tests := []struct {
		name     string
		input    flag.InternalFlag
		expected dto.DTO
	}{
		{
			name: "all fields set",
			input: flag.InternalFlag{
				BucketingKey: new("bucketKey"),
				Variations: &map[string]*any{
					"var1": new(any("var1")),
					"var2": new(any("var2")),
				},
				Rules: &[]flag.Rule{
					{
						Name:        new("rule1"),
						Query:       new("key eq \"key\""),
						Percentages: &map[string]float64{"var_true": 100, "var_false": 0},
					},
					{
						Name:  new("rule2"),
						Query: new("key eq \"key2\""),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new("var1"),
								Percentage: new(float64(30)),
								Date:       new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new("var2"),
								Percentage: new(float64(70)),
								Date:       new(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
						},
					},
					{
						Name:            new("rule3"),
						Query:           new("key eq \"key3\""),
						VariationResult: new("var2"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("var1"),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new("var2"),
						},
					},
				},
				Experimentation: &flag.ExperimentationRollout{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{"key": "value"},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
			},
			expected: dto.DTO{
				BucketingKey: new("bucketKey"),
				Variations: &map[string]*any{
					"var1": new(any("var1")),
					"var2": new(any("var2")),
				},
				Rules: &[]flag.Rule{
					{
						Name:        new("rule1"),
						Query:       new("key eq \"key\""),
						Percentages: &map[string]float64{"var_true": 100, "var_false": 0},
					},
					{
						Name:  new("rule2"),
						Query: new("key eq \"key2\""),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new("var1"),
								Percentage: new(float64(30)),
								Date:       new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new("var2"),
								Percentage: new(float64(70)),
								Date:       new(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
						},
					},
					{
						Name:            new("rule3"),
						Query:           new("key eq \"key3\""),
						VariationResult: new("var2"),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new("var1"),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new("var2"),
						},
					},
				},
				Experimentation: &dto.ExperimentationDto{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{"key": "value"},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
			},
		},
		{
			name: "nil experimentation",
			input: flag.InternalFlag{
				Experimentation: nil,
				Version:         new("v1"),
			},
			expected: dto.DTO{
				Version: new("v1"),
			},
		},
		{
			name:     "empty input",
			input:    flag.InternalFlag{},
			expected: dto.DTO{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dto.ConvertInternalFlagToDto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
