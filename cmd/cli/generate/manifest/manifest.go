package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"

	"github.com/blackbirdworks/semaphore/cmd/cli/generate/manifest/model"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	configHelper "github.com/blackbirdworks/semaphore/cmdhelpers/configfile"
	"github.com/blackbirdworks/semaphore/cmdhelpers/manifest"
	"github.com/blackbirdworks/semaphore/model/dto"
	dtoCore "github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
)

var (
	errFlagManifestDestinationIs = errors.New("--flag_manifest_destination is mandatory")
)

type Manifest struct {
	dtos                    map[string]dto.DTO
	flagManifestDestination string
}

func NewManifest(configFile, configFormat, flagManifestDestination string) (Manifest, error) {
	if flagManifestDestination == "" {
		return Manifest{}, errFlagManifestDestinationIs
	}
	flagDTOs, err := configHelper.LoadConfigFile(
		configFile,
		configFormat,
		configHelper.DefaultLocations(),
		helper.LegacyWarner(),
	)
	if err != nil {
		return Manifest{}, err
	}

	return Manifest{
		dtos:                    flagDTOs,
		flagManifestDestination: flagManifestDestination,
	}, nil
}

// Generate the manifest file.
func (m *Manifest) Generate() (helper.Output, error) {
	var captured []capturedRecord
	flags := make(map[string]flag.InternalFlag)
	for k, v := range m.dtos {
		flags[k] = dtoCore.ConvertDtoToInternalFlag(v)
	}
	output := helper.Output{}

	definitions, err := manifest.GenerateDefinitionFromInternalFlags(
		flags, manifest.WithLogger(slog.New(slogHandlerForRecords(&captured))))
	for _, rec := range captured {
		output.Add(rec.message, slogLevelToOutputLevel(rec.level))
	}
	if err != nil {
		return output, err
	}

	manifest := model.FlagManifest{
		Flags: definitions,
	}
	definitionsJSON, err := m.toJSON(manifest)
	if err != nil {
		return output, err
	}
	// if we have a destination we write the JSON in the file
	err = m.storeManifest(definitionsJSON)
	if err != nil {
		return output, err
	}

	return output.Add("🎉 Manifest has been created", helper.InfoLevel), nil
}

// toJSON will convert the definitions to a JSON string.
func (m *Manifest) toJSON(manifest model.FlagManifest) (string, error) {
	definitionsJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}

	return string(definitionsJSON), nil
}

func (m *Manifest) storeManifest(definitionsJSON string) error {
	return os.WriteFile(m.flagManifestDestination, []byte(definitionsJSON), 0600)
}

type capturedRecord struct {
	message string
	level   slog.Level
}

// captureHandler records each slog record for replay into helper.Output.
type captureHandler struct {
	records *[]capturedRecord
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	*h.records = append(*h.records, capturedRecord{level: r.Level, message: r.Message})

	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

func slogHandlerForRecords(records *[]capturedRecord) slog.Handler {
	return &captureHandler{records: records}
}

func slogLevelToOutputLevel(level slog.Level) helper.Level {
	switch {
	case level >= slog.LevelError:
		return helper.ErrorLevel
	case level >= slog.LevelWarn:
		return helper.WarnLevel
	case level >= slog.LevelInfo:
		return helper.InfoLevel
	default:
		return helper.DefaultLevel
	}
}
