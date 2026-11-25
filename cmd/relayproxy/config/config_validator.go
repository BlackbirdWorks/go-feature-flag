package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	cmdlog "github.com/blackbirdworks/semaphore/cmdhelpers/log"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
)

var (
	errEmptyConfig             = errors.New("empty config")
	errFlagSetsAndStudioCannot = errors.New(
		"flagSets and studio cannot be used together, remove flagSets from your configuration",
	)
	errEnableSharedPollerPerBucketIsOnlyAvailable = errors.New(
		"enableSharedPollerPerBucket is only available inside a flagset",
	)
	errExporterCleanQueueIntervalMustNotBe = errors.New("exporterCleanQueueInterval must not be negative")
	errUnixSocketPathMustBeSet             = errors.New(
		"unixSocketPath must be set when server mode is unixsocket",
	)
	errInvalidLogFormat                  = errors.New("invalid log format")
	errNoFlagsetsConfigured              = errors.New("no flagsets configured")
	errFlagset                           = errors.New("flagset")
	errAnAPIKeyIs                        = errors.New("an API key is used by multiple flagsets")
	errNoRetrieverAvailableIn            = errors.New("no retriever available in the configuration")
	errEnableSharedPollerPerBucketCanTBe = errors.New(
		"enableSharedPollerPerBucket can't be used with retrievers, use a single retriever",
	)
	errEnableSharedPollerPerBucketIsOnlySupported = errors.New(
		"enableSharedPollerPerBucket is only supported with a retriever of kind s3",
	)
	errEnableSharedPollerPerBucketRequiresTheS3 = errors.New(
		"enableSharedPollerPerBucket requires the s3 retriever to set item (folder mode is not supported)",
	)
)

// IsValid contains all the validation of the configuration.
func (c *Config) IsValid() error {
	if c == nil {
		return errEmptyConfig
	}

	err := validateLogLevel(c.LogLevel)
	if err != nil {
		return err
	}

	err = validateLogFormat(c.LogFormat)
	if err != nil {
		return err
	}

	err = c.validateServerConfig()
	if err != nil {
		return err
	}

	err = c.OfrepEventStream.IsValid()
	if err != nil {
		return err
	}

	err = c.Endpoints.IsValid()
	if err != nil {
		return err
	}

	err = validateExporterCleanQueueInterval(c.ExporterCleanQueueInterval)
	if err != nil {
		return err
	}

	if c.flagSetMode == flagSetModeLocal && c.Studio.URL != "" {
		return errFlagSetsAndStudioCannot
	}

	if c.EnableSharedPollerPerBucket {
		return errEnableSharedPollerPerBucketIsOnlyAvailable
	}

	if len(c.FlagSets) > 0 {
		return c.validateFlagSets()
	}

	return c.validateDefaultMode()
}

// validateExporterCleanQueueInterval rejects a negative interval (a ticker can not be created with it).
func validateExporterCleanQueueInterval(interval time.Duration) error {
	if interval < 0 {
		return errExporterCleanQueueIntervalMustNotBe
	}

	return nil
}

// validateServerConfig validates the server configuration.
func (c *Config) validateServerConfig() error {
	mode := c.ServerMode(nil)
	if mode == ServerModeUnixSocket {
		if c.UnixSocketPath() == "" {
			return errUnixSocketPathMustBeSet
		}
	}

	return nil
}

// validateLogFormat validates the log format.
func validateLogFormat(logFormat string) error {
	switch strings.ToLower(logFormat) {
	case "json", "logfmt", "":
		return nil
	default:
		return fmt.Errorf("%w %s", errInvalidLogFormat, logFormat)
	}
}

// validateLogLevel validates the log level.
func validateLogLevel(logLevel string) error {
	if logLevel == "" {
		return nil
	}

	_, err := cmdlog.ParseLevel(logLevel)
	if err != nil {
		return fmt.Errorf("validating the log level: %w", err)
	}

	return nil
}

// validateDefaultMode validates the default mode.
func (c *Config) validateDefaultMode() error {
	err := validateRetrievers(c.Retriever, c.Retrievers)
	if err != nil {
		return err
	}

	err = validateExporters(c.Exporter, c.Exporters)
	if err != nil {
		return err
	}

	return validateNotifiers(c.Notifiers)
}

// validateFlagSets validates all configured flagsets.
func (c *Config) validateFlagSets() error {
	if len(c.FlagSets) == 0 {
		return errNoFlagsetsConfigured
	}

	// Track which flagset owns each credential to ensure no duplicates across flagsets.
	// The error messages below must never contain the credential itself, only flagset names.
	ownerByCredential := make(map[string]string) // credential -> flagsetName

	for _, flagset := range c.FlagSets {
		// Validate API keys
		if len(flagset.APIKeys) == 0 {
			return fmt.Errorf("%w %s has no API keys", errFlagset, flagset.Name)
		}

		// Check for duplicate API keys across flagsets
		for _, apiKey := range flagset.APIKeys {
			if owner, exists := ownerByCredential[apiKey]; exists {
				return fmt.Errorf("%w: %s and %s", errAnAPIKeyIs, owner, flagset.Name)
			}

			ownerByCredential[apiKey] = flagset.Name
		}

		// Validate the CommonFlagSet embedded in the flagset
		err := c.validateFlagSetCommonConfig(&flagset)
		if err != nil {
			return fmt.Errorf("flagset %s: %w", flagset.Name, err)
		}
	}

	return nil
}

// validateFlagSetCommonConfig validates the CommonFlagSet configuration for a single flagset.
func (_ *Config) validateFlagSetCommonConfig(flagset *FlagSet) error {
	err := validateRetrievers(flagset.Retriever, flagset.Retrievers)
	if err != nil {
		return err
	}

	err = validateExporterCleanQueueInterval(flagset.ExporterCleanQueueInterval)
	if err != nil {
		return err
	}

	err = validateSharedPollerPerBucket(flagset)
	if err != nil {
		return err
	}

	err = validateExporters(flagset.Exporter, flagset.Exporters)
	if err != nil {
		return err
	}

	// Validate notifiers
	err = validateNotifiers(flagset.Notifiers)
	if err != nil {
		return err
	}

	return nil
}

// validateRetrievers validates the retrievers.
func validateRetrievers(retriever *retrieverconf.RetrieverConf, retrievers *[]retrieverconf.RetrieverConf) error {
	if retriever == nil && retrievers == nil {
		return errNoRetrieverAvailableIn
	}

	if retriever != nil {
		err := retriever.IsValid()
		if err != nil {
			return fmt.Errorf("validating the retriever of the configuration: %w", err)
		}
	}

	if retrievers != nil {
		for i, retriever := range *retrievers {
			err := retriever.IsValid()
			if err != nil {
				return fmt.Errorf("retriever at index %d validation failed: %w", i, err)
			}
		}
	}

	return nil
}

// validateExporters validates the exporters.
func validateExporters(exporter *ExporterConf, exporters *[]ExporterConf) error {
	if exporter != nil {
		err := exporter.IsValid()
		if err != nil {
			return err
		}
	}

	if exporters != nil {
		for i, exporter := range *exporters {
			err := exporter.IsValid()
			if err != nil {
				return fmt.Errorf("exporter at index %d validation failed: %w", i, err)
			}
		}
	}

	return nil
}

// validateNotifiers validates the notifiers.
func validateNotifiers(notifiers []NotifierConf) error {
	if len(notifiers) > 0 {
		for i, notif := range notifiers {
			err := notif.IsValid()
			if err != nil {
				return fmt.Errorf("notifier at index %d validation failed: %w", i, err)
			}
		}
	}

	return nil
}

// validateSharedPollerPerBucket checks that a flagset using enableSharedPollerPerBucket has a single S3 retriever
// in single file mode.
func validateSharedPollerPerBucket(flagset *FlagSet) error {
	if !flagset.EnableSharedPollerPerBucket {
		return nil
	}

	if flagset.Retrievers != nil {
		return errEnableSharedPollerPerBucketCanTBe
	}

	r := flagset.Retriever
	if r == nil || r.Kind != retrieverconf.S3Retriever {
		return errEnableSharedPollerPerBucketIsOnlySupported
	}

	if r.Item == "" {
		return errEnableSharedPollerPerBucketRequiresTheS3
	}

	return nil
}
