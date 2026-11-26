// Package bootstrap defines the version 2 bootstrap file of the relay proxy: its schema, a strict loader that
// reports path- and line-addressed errors, the secret references, the explicit environment overrides, the
// defaults table, the redacted effective print and the JSON Schema.
//
// The package is built beside the running relay: nothing in the relay reads it yet. The version 1 configuration in
// cmd/relayproxy/config stays untouched.
package bootstrap
