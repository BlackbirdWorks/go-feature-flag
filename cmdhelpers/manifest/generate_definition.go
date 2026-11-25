package manifest

import (
	"fmt"
	"log/slog"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmdhelpers/manifest/model"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

// Option configures the generation of the flag definitions.
type Option func(*options)

type options struct {
	logger *slog.Logger
}

// WithLogger sets the logger that receives the warnings and errors about the flags that are
// ignored. Without it nothing is logged.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

func newOptions(opts []Option) options {
	o := options{logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

// GenerateDefinitionFromFlags builds the flag definitions of the manifest from flags.
func GenerateDefinitionFromFlags(flags map[string]flag.Flag, opts ...Option) (
	map[string]model.FlagDefinition, error) {
	logger := newOptions(opts).logger
	definitions := make(map[string]model.FlagDefinition)

	for flagName, rawFlag := range flags {
		internalFlag, ok := rawFlag.(*flag.InternalFlag)
		if !ok || internalFlag == nil {
			logger.Error("unexpected flag type, expected InternalFlag", slog.String("flag", flagName))

			continue
		}

		if internalFlag.Variations == nil {
			logger.Error("flag ignored: no variations provided", slog.String("flag", flagName))

			continue
		}

		flagType, err := helper.FlagTypeFromVariations(*internalFlag.Variations)
		if err != nil {
			return definitions, fmt.Errorf("invalid configuration for flag %s: %w", flagName, err)
		}

		metadata := internalFlag.Metadata
		if metadata == nil {
			m := make(map[string]any)
			metadata = &m
		}

		defaultValue, ok := (*metadata)["defaultValue"]
		if !ok {
			logger.Warn("flag ignored: no default value provided", slog.String("flag", flagName))

			continue
		}

		description, ok := (*metadata)["description"].(string)
		if !ok {
			description = ""
		}

		definition := model.FlagDefinition{
			FlagType:     flagType,
			DefaultValue: defaultValue,
			Description:  description,
		}
		definitions[flagName] = definition
	}

	return definitions, nil
}

// GenerateDefinitionFromInternalFlags builds the flag definitions of the manifest from internal flags.
func GenerateDefinitionFromInternalFlags(flags map[string]flag.InternalFlag, opts ...Option) (
	map[string]model.FlagDefinition, error) {
	asFlag := make(map[string]flag.Flag, len(flags))
	for name := range flags {
		f := flags[name]
		asFlag[name] = &f
	}

	return GenerateDefinitionFromFlags(asFlag, opts...)
}
