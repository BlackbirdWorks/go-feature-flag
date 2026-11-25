package dto

import (
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// ConvertDtoToInternalFlag is converting a DTO to a flag.InternalFlag.
func ConvertDtoToInternalFlag(dto *DTO) flag.InternalFlag {
	var experimentation *flag.ExperimentationRollout
	if dto.Experimentation != nil {
		experimentation = &flag.ExperimentationRollout{
			Start: dto.Experimentation.Start,
			End:   dto.Experimentation.End,
		}
	}

	return flag.InternalFlag{
		BucketingKey:    dto.BucketingKey,
		Variations:      dto.Variations,
		Rules:           dto.Rules,
		DefaultRule:     dto.DefaultRule,
		TrackEvents:     dto.TrackEvents,
		Disable:         dto.Disable,
		Version:         dto.Version,
		Scheduled:       dto.Scheduled,
		Experimentation: experimentation,
		Metadata:        dto.Metadata,
	}
}

// ConvertInternalFlagToDto converts an internal flag to its DTO representation.
func ConvertInternalFlagToDto(internalFlag *flag.InternalFlag) DTO {
	var experimentation *ExperimentationDto
	if internalFlag.Experimentation != nil {
		experimentation = &ExperimentationDto{
			Start: internalFlag.Experimentation.Start,
			End:   internalFlag.Experimentation.End,
		}
	}

	return DTO{
		TrackEvents:     internalFlag.TrackEvents,
		Disable:         internalFlag.Disable,
		Version:         internalFlag.Version,
		Converter:       nil,
		Variations:      internalFlag.Variations,
		Rules:           internalFlag.Rules,
		BucketingKey:    internalFlag.BucketingKey,
		DefaultRule:     internalFlag.DefaultRule,
		Scheduled:       internalFlag.Scheduled,
		Experimentation: experimentation,
		Metadata:        internalFlag.Metadata,
	}
}
