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
// ignored. The default is slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

func newOptions(opts []Option) options {
	o := options{logger: slog.Default()}
	for _, opt := range opts {
		opt(&o)
	}

	return o
}

func GenerateDefinitionFromFlags(flags map[string]flag.Flag, opts ...Option) (
	map[string]model.FlagDefinition, error) {
	logger := newOptions(opts).logger
	definitions := make(map[string]model.FlagDefinition)

	for k, v := range flags {
		v, ok := v.(*flag.InternalFlag)
		if !ok || v == nil {
			logger.Error(fmt.Sprintf("unexpected flag type for key %q, expected InternalFlag", k))

			continue
		}
		if v.Variations == nil {
			logger.Error(fmt.Sprintf("flag %s ignored: no variations provided", k))

			continue
		}
		flagType, err := helper.FlagTypeFromVariations(*v.Variations)
		if err != nil {
			return definitions, fmt.Errorf("invalid configuration for flag %s: %w", k, err)
		}

		metadata := v.Metadata
		if metadata == nil {
			m := make(map[string]any)
			metadata = &m
		}

		defaultValue, ok := (*metadata)["defaultValue"]
		if !ok {
			logger.Warn(fmt.Sprintf("flag %s ignored: no default value provided", k))

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
		definitions[k] = definition
	}

	return definitions, nil
}

func GenerateDefinitionFromInternalFlags(flags map[string]flag.InternalFlag, opts ...Option) (
	map[string]model.FlagDefinition, error) {
	asFlag := make(map[string]flag.Flag, len(flags))
	for name := range flags {
		f := flags[name]
		asFlag[name] = &f
	}

	return GenerateDefinitionFromFlags(asFlag, opts...)
}
