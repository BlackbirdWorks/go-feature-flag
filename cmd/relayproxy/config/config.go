package config

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/spf13/pflag"
)

// Config is the configuration of the relay proxy.
type Config struct {
	// apiKeySet is the internal representation of an API keys list configured
	// we store them in a set to be
	apiKeysSet map[string]APIKeyType
	// EvaluationContextEnrichment (optional) will be merged with the evaluation context sent during the evaluation.
	// It is useful to add common attributes to all the evaluations, such as a server version, environment, ...
	//
	// All those fields will be included in the custom attributes of the evaluation context,
	// if in the evaluation context you have a field with the same name,
	// it will be overridden by the evaluationContextEnrichment.
	// Default: nil
	//nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
	EvaluationContextEnrichment map[string]any `koanf:"evaluationcontextenrichment" mapstructure:"evaluationContextEnrichment"`
	// logger is the logger for the relay proxy
	logger *slog.Logger
	// configLoader is the service in charge of loading the configuration.
	configLoader *Loader
	// PersistentFlagConfigurationFile (optional) if set Semaphore will store flags configuration in this file
	//  to be able to serve the flags even if none of the retrievers is available during starting time.
	//
	// By default, the flag configuration is not persisted and stays on the retriever system. By setting a file here,
	// you ensure that Semaphore will always start with a configuration but which can be out-dated.
	//nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped
	PersistentFlagConfigurationFile string `koanf:"persistentflagconfigurationfile" mapstructure:"persistentFlagConfigurationFile"`
	// Version is the version of the relay-proxy
	Version string `koanf:"version" mapstructure:"version"`
	// EnvVariablePrefix (optional) is the prefix we are using to load the environment variables
	// By default we have no prefix
	EnvVariablePrefix string `koanf:"envvariableprefix" mapstructure:"envVariablePrefix"`
	// LogFormat (optional) sets the log message format
	// Possible values: json, logfmt
	// Default: json
	LogFormat string `koanf:"logformat" mapstructure:"logFormat"`
	// Deprecated: use Swagger.Host instead
	Host string `koanf:"host" mapstructure:"host"`
	// AwsLambdaAdapter (optional) is the adapter to use when the relay proxy is started as an AWS Lambda.
	// Possible values are "APIGatewayV1", "APIGatewayV2" and "ALB"
	// Default: "APIGatewayV2"
	//
	// Deprecated: use `Server.LambdaAdapter` instead
	AwsLambdaAdapter string `koanf:"awslambdaadapter" mapstructure:"awsLambdaAdapter"`
	// flagSetMode is where the flagSets come from.
	flagSetMode flagSetMode
	// AwsAPIGatewayBasePath (optional) is the base path prefix for AWS API Gateway deployments.
	// This is useful when deploying behind a non-root path like "/api" or "/dev/feature-flags".
	// The relay proxy will strip this base path from incoming requests before processing.
	// Example: if set to "/api/feature-flags", requests to "/api/feature-flags/health" will be processed as "/health"
	// Default: ""
	//
	// Deprecated: use `Server.AwsAPIGatewayBasePath` instead
	AwsAPIGatewayBasePath string `koanf:"awsapigatewaybasepath" mapstructure:"awsApiGatewayBasePath"`
	// LogLevel (optional) sets the verbosity for logging,
	// Possible values: debug, info, warn, error, dpanic, panic, fatal
	// If level debug semaphore relay proxy will run on debug mode, with more logs and custom responses
	// Default: debug
	LogLevel string `koanf:"loglevel" mapstructure:"logLevel"`
	// OpenTelemetryOtlpEndpoint (optional) is the endpoint of the OpenTelemetry collector
	// Default: ""
	OpenTelemetryOtlpEndpoint string `koanf:"opentelemetryotlpendpoint" mapstructure:"openTelemetryOtlpEndpoint"`
	// OtelConfig is the configuration for the OpenTelemetry part of the relay proxy
	OtelConfig OpenTelemetryConfiguration `koanf:"otel" mapstructure:"otel"`
	// Studio (optional) loads the FlagSets from Semaphore Studio instead of the configuration file.
	// It is enabled when Studio.URL is set.
	Studio Studio `koanf:"studio" mapstructure:"studio"`
	// AuthorizedKeys list of API keys that authorized to use endpoints
	AuthorizedKeys APIKeys `koanf:"authorizedkeys" mapstructure:"authorizedKeys"`
	// JaegerConfig is the configuration for the Jaeger sampling of the relay proxy
	JaegerConfig JaegerSamplerConfiguration `koanf:"jaeger" mapstructure:"jaeger"`
	// FlagSets is the list of flag sets configured.
	// A flag set is a group of flags that can be used to configure the relay proxy.
	// Each flag set can have its own API key, retrievers, notifiers and exporters.
	// There is no inheritance between flag sets.
	FlagSets []FlagSet `koanf:"flagsets" mapstructure:"flagsets"`
	// Swagger is the swagger configuration
	Swagger Swagger `koanf:"swagger" mapstructure:"swagger"`
	// OfrepEventStream (optional) advertises the flag-change SSE endpoint in the OFREP bulk
	// evaluation response so that providers can subscribe to changes instead of polling.
	// It is enabled when OfrepEventStream.BaseURL is set.
	OfrepEventStream OfrepEventStream `koanf:"ofrepeventstream" mapstructure:"ofrepEventStream"`
	// Deprecated: use AuthorizedKeys instead
	// APIKeys list of API keys that authorized to use endpoints
	APIKeys []string `koanf:"apikeys" mapstructure:"apiKeys"`
	// Server is the server configuration, including host, port, and unix socket
	Server Server `koanf:"server" mapstructure:"server"`
	// CommonFlagSet is the common flag set for the relay proxy
	//nolint:embeddedstructfieldcheck // fieldalignment needs the embedded struct here
	CommonFlagSet `koanf:",squash" mapstructure:",inline"`
	// Endpoints (optional) configures the endpoints of the relay proxy.
	Endpoints Endpoints `koanf:"endpoints" mapstructure:"endpoints"`
	// MonitoringPort (optional) is the port we are using to expose the metrics and healthchecks
	// If not set we will use the same port as the proxy
	//
	// Deprecated: use Server.MonitoringPort instead
	MonitoringPort int `koanf:"monitoringport" mapstructure:"monitoringPort"`
	// ---------- Deprecated fields ----------
	// ListenPort (optional) is the port we are using to start the proxy
	//
	// Deprecated: use Server.Port instead
	ListenPort int `koanf:"listen" mapstructure:"listen"`
	// ---------- End of deprecated fields ----------
	// ---------- Private fields ----------
	// mutex is used to protect the configuration from concurrent access during runtime
	// it applies to the following fields:
	// - AuthorizedKeys
	// - APIKeys
	// - FlagSets
	// - apiKeysSet
	// - forceAuthenticatedRequests
	// - hadFlagSets
	mutex sync.RWMutex
	// DisableSemaphoreMetadata (optional) stops adding semaphore_cacheable to the metadata of the OFREP
	// evaluation responses, only the legacy gofeatureflag_cacheable is sent. It is for the clients that
	// compare the metadata strictly.
	// Default: false
	DisableSemaphoreMetadata bool `koanf:"disablesemaphoremetadata" mapstructure:"disableSemaphoreMetadata"`
	// StartAsAwsLambda (optional) if true, the relay proxy will start ready to be launched as AWS Lambda
	//
	// Deprecated: use `Server.Mode = lambda` instead
	StartAsAwsLambda bool `koanf:"startasawslambda" mapstructure:"startAsAwsLambda"`
	// Disable the x-semaphore-version header, and its legacy name x-gofeatureflag-version, in the
	// relay-proxy HTTP response
	// Default: false
	DisableVersionHeader bool `koanf:"disableversionheader" mapstructure:"disableVersionHeader"`
	// DisableFlagDetailsInStream (optional) removes flag definitions from WebSocket change payloads.
	// Default: false
	DisableFlagDetailsInStream bool `koanf:"disableflagdetailsinstream" mapstructure:"disableFlagDetailsInStream"`
	// Deprecated: use Swagger.Enabled instead
	EnableSwagger bool `koanf:"enableswagger" mapstructure:"enableSwagger"`
	// EnablePprof (optional) if true, semaphore relay proxy will start
	// the pprof endpoints on the same port as the monitoring.
	// Default: false
	EnablePprof bool `koanf:"enablepprof" mapstructure:"enablePprof"`
	// hadFlagSets latches to true once the configuration has had a flag set: the relay proxy cannot switch
	// between default and flag set mode at runtime, so a reload that leaves no running flag set (their
	// replacement failed to start) must not turn the top-level evaluation keys valid again.
	hadFlagSets bool
	// forceAuthenticatedRequests is true if we have at least 1 AuthorizedKey.Evaluation key set.
	forceAuthenticatedRequests bool
	// HideBanner (optional) if true, we don't display the semaphore relay proxy banner
	HideBanner bool `koanf:"hidebanner" mapstructure:"hideBanner"`
	// EnableBulkMetricFlagNames (optional) enables per-flag metrics for bulk evaluation endpoints.
	// This adds flag_name labels to the all_flags_evaluations_with_flag_total metric.
	// Default: false
	EnableBulkMetricFlagNames bool `koanf:"enablebulkmetricflagnames" mapstructure:"enableBulkMetricFlagNames"`
	// ExposeLegacyMetrics (optional) also exposes every semaphore_* Prometheus series under its
	// legacy gofeatureflag_* name, so dashboards and alerts written for GO Feature Flag keep working.
	// Default: false
	ExposeLegacyMetrics bool `koanf:"exposelegacymetrics" mapstructure:"exposeLegacyMetrics"`
	// ---------- End of private fields ----------
}

// New is reading the configuration file.
func New(cmdLineFlagSet *pflag.FlagSet, log *slog.Logger, version string, opts ...LoaderOption) (*Config, error) {
	// Map environment variables
	configLoader := NewConfigLoader(cmdLineFlagSet, log, version, true, opts...)

	proxyConf, errUnmarshal := configLoader.ToConfig()
	if errUnmarshal != nil {
		return nil, errUnmarshal
	}

	proxyConf.configLoader = configLoader
	proxyConf.logger = log
	proxyConf.preloadAPIKeys()

	return proxyConf, nil
}

// IsDebugEnabled returns true if the log level is debug.
func (c *Config) IsDebugEnabled() bool {
	if c == nil {
		return false
	}

	return strings.ToLower(c.LogLevel) == "debug"
}

// IsUsingFlagsets returns true if the configuration is using flagsets.
func (c *Config) IsUsingFlagsets() bool {
	c.mutex.RLock()
	defer c.mutex.RUnlock()

	return len(c.FlagSets) > 0
}

// SetAPIKeys replaces the API keys of the configuration.
//
// Deprecated: use SetAuthorizedKeys instead.
func (c *Config) SetAPIKeys(apiKeys []string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.APIKeys = apiKeys
}

// SetAuthorizedKeys sets the authorized keys for the configuration.
func (c *Config) SetAuthorizedKeys(authorizedKeys APIKeys) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.AuthorizedKeys = authorizedKeys
}

// GetAuthorizedKeys returns a copy of the AuthorizedKeys with proper synchronization.
func (c *Config) GetAuthorizedKeys() APIKeys {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	// Return a deep copy to avoid external modification
	return APIKeys{
		Admin:      append([]string(nil), c.AuthorizedKeys.Admin...),
		Evaluation: append([]string(nil), c.AuthorizedKeys.Evaluation...),
	}
}

// GetAPIKeys returns a copy of the APIKeys with proper synchronization.
func (c *Config) GetAPIKeys() []string {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	// Return a copy to avoid external modification
	result := make([]string, len(c.APIKeys))
	copy(result, c.APIKeys)

	return result
}
