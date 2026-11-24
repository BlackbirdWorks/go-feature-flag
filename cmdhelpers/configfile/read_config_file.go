package configfile

import (
	"fmt"
	"os"
	"strings"

	"github.com/blackbirdworks/semaphore/internal/cache"
	"github.com/blackbirdworks/semaphore/modules/core/dto"
	"github.com/blackbirdworks/semaphore/modules/core/legacy"
)

// ConfigFileDefaultLocations returns the folders searched for the flag file when no path is given.
func ConfigFileDefaultLocations() []string {
	return []string{
		"./",
		"/semaphore/",
		"/etc/opt/semaphore/",
		"/goff/",
		"/etc/opt/goff/",
	}
}

// flagFileNames returns the names of the flag file searched in the default locations, in this order. The
// first one is the name of Semaphore.
func flagFileNames() []string {
	return []string{"flags.semaphore", "flags.goff"}
}

// LoadConfigFile reads the flag file at inputFilePath, or the first one found in defaultLocations (the
// default locations when nil). The use of the legacy file name is reported through warner.
func LoadConfigFile(
	inputFilePath, configFormat string,
	defaultLocations []string,
	warner *legacy.Warner,
) (map[string]dto.DTO, error) {
	if defaultLocations == nil {
		defaultLocations = ConfigFileDefaultLocations()
	}
	fileNames := flagFileNames()
	supportedExtensions := []string{
		"yaml",
		"toml",
		"json",
		"yml",
	}

	if inputFilePath != "" {
		if _, err := os.Stat(inputFilePath); err != nil {
			return nil, fmt.Errorf("impossible to find config file %s", inputFilePath)
		}

		return readConfigFile(inputFilePath, configFormat)
	}
	for _, location := range defaultLocations {
		for _, filename := range fileNames {
			for _, ext := range supportedExtensions {
				configFile := fmt.Sprintf("%s%s.%s", location, filename, ext)
				if _, err := os.Stat(configFile); err != nil {
					continue
				}
				if filename != fileNames[0] {
					warner.Warn(legacy.KindFlagFile, fmt.Sprintf(
						"the flag file %s uses the legacy name %s, it is still accepted, rename it to %s.%s",
						configFile, filename, fileNames[0], ext))
				}

				return readConfigFile(configFile, ext)
			}
		}
	}

	return nil, fmt.Errorf(
		"impossible to find config file in the default locations [%s]",
		strings.Join(defaultLocations, ","),
	)
}

func readConfigFile(configFile, configFormat string) (map[string]dto.DTO, error) {
	dat, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}
	flags, err := ParseConfig(dat, configFormat)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", configFile, err)
	}

	return flags, nil
}

// ParseConfig parses the content of a flag file in the given format (toml, json or yaml).
// Any format other than toml or json is parsed as YAML.
// It uses the same parser as the retrievers.
func ParseConfig(data []byte, configFormat string) (map[string]dto.DTO, error) {
	flags, err := cache.ConvertToFlagStruct(data, configFormat)
	if err != nil {
		format := strings.ToLower(configFormat)
		if format != "toml" && format != "json" {
			format = "yaml"
		}

		return nil, fmt.Errorf("could not parse file (%s): %w", format, err)
	}

	return flags, nil
}
