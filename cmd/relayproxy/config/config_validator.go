package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
)

// IsValid contains all the validation of the configuration.
func (c *Config) IsValid() error {
	if c == nil {
		return fmt.Errorf("empty config")
	}
	if err := validateLogLevel(c.LogLevel); err != nil {
		return err
	}
	if err := validateLogFormat(c.LogFormat); err != nil {
		return err
	}
	if err := c.validateServerConfig(); err != nil {
		return err
	}
	if err := c.OfrepEventStream.IsValid(); err != nil {
		return err
	}
	if err := c.Endpoints.IsValid(); err != nil {
		return err
	}
	if err := validateExporterCleanQueueInterval(c.ExporterCleanQueueInterval); err != nil {
		return err
	}
	if c.flagSetMode == flagSetModeLocal && c.Studio.URL != "" {
		return errors.New("flagSets and studio cannot be used together, remove flagSets from your configuration")
	}
	if c.EnableSharedPollerPerBucket {
		return errors.New("enableSharedPollerPerBucket is only available inside a flagset")
	}
	if len(c.FlagSets) > 0 {
		return c.validateFlagSets()
	}
	return c.validateDefaultMode()
}

// validateExporterCleanQueueInterval rejects a negative interval (a ticker can not be created with it).
func validateExporterCleanQueueInterval(interval time.Duration) error {
	if interval < 0 {
		return errors.New("exporterCleanQueueInterval must not be negative")
	}
	return nil
}

// validateServerConfig validates the server configuration
func (c *Config) validateServerConfig() error {
	mode := c.ServerMode(nil)
	if mode == ServerModeUnixSocket {
		if c.UnixSocketPath() == "" {
			return errors.New("unixSocketPath must be set when server mode is unixsocket")
		}
	}
	return nil
}

// validateLogFormat validates the log format
func validateLogFormat(logFormat string) error {
	switch strings.ToLower(logFormat) {
	case "json", "logfmt", "":
		return nil
	default:
		return fmt.Errorf("invalid log format %s", logFormat)
	}
}

// validateLogLevel validates the log level
func validateLogLevel(logLevel string) error {
	if logLevel == "" {
		return nil
	}
	if _, err := zapcore.ParseLevel(logLevel); err != nil {
		return err
	}
	return nil
}

// validateDefaultMode validates the default mode
func (c *Config) validateDefaultMode() error {
	if err := validateRetrievers(c.Retriever, c.Retrievers); err != nil {
		return err
	}
	if err := validateExporters(c.Exporter, c.Exporters); err != nil {
		return err
	}
	return validateNotifiers(c.Notifiers)
}

// validateFlagSets validates all configured flagsets
func (c *Config) validateFlagSets() error {
	if len(c.FlagSets) == 0 {
		return fmt.Errorf("no flagsets configured")
	}

	// Track which flagset owns each credential to ensure no duplicates across flagsets.
	// The error messages below must never contain the credential itself, only flagset names.
	ownerByCredential := make(map[string]string) // credential -> flagsetName

	for _, flagset := range c.FlagSets {
		// Validate API keys
		if len(flagset.APIKeys) == 0 {
			return fmt.Errorf("flagset %s has no API keys", flagset.Name)
		}

		// Check for duplicate API keys across flagsets
		for _, apiKey := range flagset.APIKeys {
			if owner, exists := ownerByCredential[apiKey]; exists {
				return fmt.Errorf("an API key is used by multiple flagsets: %s and %s", owner, flagset.Name)
			}
			ownerByCredential[apiKey] = flagset.Name
		}

		// Validate the CommonFlagSet embedded in the flagset
		if err := c.validateFlagSetCommonConfig(&flagset); err != nil {
			return fmt.Errorf("flagset %s: %w", flagset.Name, err)
		}
	}

	return nil
}

// validateFlagSetCommonConfig validates the CommonFlagSet configuration for a single flagset
func (c *Config) validateFlagSetCommonConfig(flagset *FlagSet) error {
	if err := validateRetrievers(flagset.Retriever, flagset.Retrievers); err != nil {
		return err
	}
	if err := validateExporterCleanQueueInterval(flagset.ExporterCleanQueueInterval); err != nil {
		return err
	}
	if err := validateSharedPollerPerBucket(flagset); err != nil {
		return err
	}

	if err := validateExporters(flagset.Exporter, flagset.Exporters); err != nil {
		return err
	}

	// Validate notifiers
	if err := validateNotifiers(flagset.Notifiers); err != nil {
		return err
	}
	return nil
}

// validateRetrievers validates the retrievers
func validateRetrievers(retriever *retrieverconf.RetrieverConf, retrievers *[]retrieverconf.RetrieverConf) error {
	if retriever == nil && retrievers == nil {
		return fmt.Errorf("no retriever available in the configuration")
	}
	if retriever != nil {
		if err := retriever.IsValid(); err != nil {
			return err
		}
	}

	if retrievers != nil {
		for i, retriever := range *retrievers {
			if err := retriever.IsValid(); err != nil {
				return fmt.Errorf("retriever at index %d validation failed: %w", i, err)
			}
		}
	}
	return nil
}

// validateExporters validates the exporters
func validateExporters(exporter *ExporterConf, exporters *[]ExporterConf) error {
	if exporter != nil {
		if err := exporter.IsValid(); err != nil {
			return err
		}
	}
	if exporters != nil {
		for i, exporter := range *exporters {
			if err := exporter.IsValid(); err != nil {
				return fmt.Errorf("exporter at index %d validation failed: %w", i, err)
			}
		}
	}
	return nil
}

// validateNotifiers validates the notifiers
func validateNotifiers(notifiers []NotifierConf) error {
	if len(notifiers) > 0 {
		for i, notif := range notifiers {
			if err := notif.IsValid(); err != nil {
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
		return errors.New("enableSharedPollerPerBucket can't be used with retrievers, use a single retriever")
	}
	r := flagset.Retriever
	if r == nil || r.Kind != retrieverconf.S3Retriever {
		return errors.New("enableSharedPollerPerBucket is only supported with a retriever of kind s3")
	}
	if r.Item == "" {
		return errors.New("enableSharedPollerPerBucket requires the s3 retriever to set item (folder mode is not supported)")
	}
	return nil
}
