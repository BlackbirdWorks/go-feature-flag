package dto_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

const (
	bucketKey = "bucketKey"
	key       = "key"
	keyEqKey  = "key eq \"key\""
	keyEqKey2 = "key eq \"key2\""
	keyEqKey3 = "key eq \"key3\""
	rule1     = "rule1"
	rule2     = "rule2"
	rule3     = "rule3"
	value     = "value"
	var12     = "var1"
	var22     = "var2"
	varFalse  = "var_false"
	varTrue   = "var_true"
)

func TestConvertV1DtoToInternalFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    dto.DTO
		expected flag.InternalFlag
		name     string
	}{
		{
			name: "all fields set",
			input: dto.DTO{
				BucketingKey: new(bucketKey),
				Variations: &map[string]*any{
					var12: new(any(var12)),
					var22: new(any(var22)),
				},
				Rules: &[]flag.Rule{{
					Name:        new(rule1),
					Query:       new(keyEqKey),
					Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
					{
						Name:  new(rule2),
						Query: new(keyEqKey2),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(var12),
								Percentage: new(float64(30)),
								Date: new(
									time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(var22),
								Percentage: new(float64(70)),
								Date: new(
									time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
						},
					},
					{
						Name:            new(rule3),
						Query:           new(keyEqKey3),
						VariationResult: new(var22),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(var12),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(var22),
						},
					},
				},
				Experimentation: &dto.ExperimentationDto{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{key: value},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
			},
			expected: flag.InternalFlag{
				BucketingKey: new(bucketKey),
				Variations: &map[string]*any{
					var12: new(any(var12)),
					var22: new(any(var22)),
				},
				Rules: &[]flag.Rule{{
					Name:        new(rule1),
					Query:       new(keyEqKey),
					Percentages: &map[string]float64{varTrue: 100, varFalse: 0}},
					{
						Name:  new(rule2),
						Query: new(keyEqKey2),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(var12),
								Percentage: new(float64(30)),
								Date: new(
									time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(var22),
								Percentage: new(float64(70)),
								Date: new(
									time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
						},
					},
					{
						Name:            new(rule3),
						Query:           new(keyEqKey3),
						VariationResult: new(var22),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(var12),
				},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(var22),
						},
					},
				},
				Experimentation: &flag.ExperimentationRollout{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata: &map[string]any{key: value},
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
			t.Parallel()

			result := tt.input.Convert()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConvertInternalFlagToDto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expected dto.DTO
		input    flag.InternalFlag
		name     string
	}{
		{
			name: "all fields set",
			input: flag.InternalFlag{
				BucketingKey: new(bucketKey),
				Variations: &map[string]*any{
					var12: new(any(var12)),
					var22: new(any(var22)),
				},
				Rules: &[]flag.Rule{
					{
						Name:        new(rule1),
						Query:       new(keyEqKey),
						Percentages: &map[string]float64{varTrue: 100, varFalse: 0},
					},
					{
						Name:  new(rule2),
						Query: new(keyEqKey2),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(var12),
								Percentage: new(float64(30)),
								Date:       new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(var22),
								Percentage: new(float64(70)),
								Date:       new(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
						},
					},
					{
						Name:            new(rule3),
						Query:           new(keyEqKey3),
						VariationResult: new(var22),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(var12),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(var22),
						},
					},
				},
				Experimentation: &flag.ExperimentationRollout{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{key: value},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
			},
			expected: dto.DTO{
				BucketingKey: new(bucketKey),
				Variations: &map[string]*any{
					var12: new(any(var12)),
					var22: new(any(var22)),
				},
				Rules: &[]flag.Rule{
					{
						Name:        new(rule1),
						Query:       new(keyEqKey),
						Percentages: &map[string]float64{varTrue: 100, varFalse: 0},
					},
					{
						Name:  new(rule2),
						Query: new(keyEqKey2),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(var12),
								Percentage: new(float64(30)),
								Date:       new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(var22),
								Percentage: new(float64(70)),
								Date:       new(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
						},
					},
					{
						Name:            new(rule3),
						Query:           new(keyEqKey3),
						VariationResult: new(var22),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(var12),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(var22),
						},
					},
				},
				Experimentation: &dto.ExperimentationDto{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{key: value},
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
			t.Parallel()

			result := dto.ConvertInternalFlagToDto(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
