package api

import "github.com/blackbirdworks/semaphore/cmd/relayproxy/config"

// lambdaSettings is the settings of a server that only needs the lambda options of the configuration: the
// internal tests can not use the apiwire package, which imports this one.
func lambdaSettings(cfg *config.Config) *Settings {
	return &Settings{
		Mode:                  cfg.ServerMode(nil),
		LambdaAdapter:         cfg.LambdaAdapter(nil),
		AwsAPIGatewayBasePath: cfg.EffectiveAwsAPIGatewayBasePath(nil),
	}
}
