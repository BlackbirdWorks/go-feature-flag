// Package apiwire builds the narrow settings of the HTTP layer from the v1 configuration of the relay proxy.
//
// The api package and the handlers do not import the configuration: they read an immutable [api.Settings] and, for
// the API keys that change at reload, a [service.APIKeys]. This package is the one place that knows both sides, so
// the startup wiring (main) and the tests build a server from a [config.Config] the same way.
package apiwire
