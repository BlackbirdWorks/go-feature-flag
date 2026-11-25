package dto_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	bucketKey     = "bucketKey"
	keyEqKeyTwo   = "key eq \"key2\""
	keyEqKeyThree = "key eq \"key3\""
	ruleThree     = "rule3"
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
					fixtures.VarOne: new(any(fixtures.VarOne)),
					fixtures.VarTwo: new(any(fixtures.VarTwo)),
				},
				Rules: &[]flag.Rule{{
					Name:        new(fixtures.RuleOne),
					Query:       new(fixtures.QueryKeyEqKey),
					Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
					{
						Name:  new(fixtures.RuleTwo),
						Query: new(keyEqKeyTwo),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarOne),
								Percentage: new(float64(30)),
								Date: new(
									time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarTwo),
								Percentage: new(float64(70)),
								Date: new(
									time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
						},
					},
					{
						Name:            new(ruleThree),
						Query:           new(keyEqKeyThree),
						VariationResult: new(fixtures.VarTwo),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VarOne),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarTwo),
						},
					},
				},
				Experimentation: &dto.ExperimentationDto{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{fixtures.Key: fixtures.Value},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
			},
			expected: flag.InternalFlag{
				BucketingKey: new(bucketKey),
				Variations: &map[string]*any{
					fixtures.VarOne: new(any(fixtures.VarOne)),
					fixtures.VarTwo: new(any(fixtures.VarTwo)),
				},
				Rules: &[]flag.Rule{{
					Name:        new(fixtures.RuleOne),
					Query:       new(fixtures.QueryKeyEqKey),
					Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0}},
					{
						Name:  new(fixtures.RuleTwo),
						Query: new(keyEqKeyTwo),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarOne),
								Percentage: new(float64(30)),
								Date: new(
									time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarTwo),
								Percentage: new(float64(70)),
								Date: new(
									time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC),
								),
							},
						},
					},
					{
						Name:            new(ruleThree),
						Query:           new(keyEqKeyThree),
						VariationResult: new(fixtures.VarTwo),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VarOne),
				},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarTwo),
						},
					},
				},
				Experimentation: &flag.ExperimentationRollout{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata: &map[string]any{fixtures.Key: fixtures.Value},
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
					fixtures.VarOne: new(any(fixtures.VarOne)),
					fixtures.VarTwo: new(any(fixtures.VarTwo)),
				},
				Rules: &[]flag.Rule{
					{
						Name:        new(fixtures.RuleOne),
						Query:       new(fixtures.QueryKeyEqKey),
						Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0},
					},
					{
						Name:  new(fixtures.RuleTwo),
						Query: new(keyEqKeyTwo),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarOne),
								Percentage: new(float64(30)),
								Date:       new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarTwo),
								Percentage: new(float64(70)),
								Date:       new(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
						},
					},
					{
						Name:            new(ruleThree),
						Query:           new(keyEqKeyThree),
						VariationResult: new(fixtures.VarTwo),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VarOne),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarTwo),
						},
					},
				},
				Experimentation: &flag.ExperimentationRollout{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{fixtures.Key: fixtures.Value},
				TrackEvents: new(true),
				Disable:     new(false),
				Version:     new("v1"),
			},
			expected: dto.DTO{
				BucketingKey: new(bucketKey),
				Variations: &map[string]*any{
					fixtures.VarOne: new(any(fixtures.VarOne)),
					fixtures.VarTwo: new(any(fixtures.VarTwo)),
				},
				Rules: &[]flag.Rule{
					{
						Name:        new(fixtures.RuleOne),
						Query:       new(fixtures.QueryKeyEqKey),
						Percentages: &map[string]float64{fixtures.VarTrue: 100, fixtures.VarFalse: 0},
					},
					{
						Name:  new(fixtures.RuleTwo),
						Query: new(keyEqKeyTwo),
						ProgressiveRollout: &flag.ProgressiveRollout{
							Initial: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarOne),
								Percentage: new(float64(30)),
								Date:       new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
							End: &flag.ProgressiveRolloutStep{
								Variation:  new(fixtures.VarTwo),
								Percentage: new(float64(70)),
								Date:       new(time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)),
							},
						},
					},
					{
						Name:            new(ruleThree),
						Query:           new(keyEqKeyThree),
						VariationResult: new(fixtures.VarTwo),
					},
				},
				DefaultRule: &flag.Rule{
					VariationResult: new(fixtures.VarOne),
				},
				Scheduled: &[]flag.ScheduledStep{
					{
						Date: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
						DefaultRule: &flag.Rule{
							VariationResult: new(fixtures.VarTwo),
						},
					},
				},
				Experimentation: &dto.ExperimentationDto{
					Start: new(time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)),
					End:   new(time.Date(2022, 1, 2, 0, 0, 0, 0, time.UTC)),
				},
				Metadata:    &map[string]any{fixtures.Key: fixtures.Value},
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
