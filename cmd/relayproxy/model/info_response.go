package model

import "time"

// InfoResponse is the object returned by the info API.
type InfoResponse struct {
	// LatestCacheRefresh is the last time when your flag file was read and stored in the internal cache.
	// This field is used for backward compatibility when using the default flagset.
	LatestCacheRefresh *time.Time `example:"2022-06-13T11:22:55.941628+02:00" json:"cacheRefresh,omitempty"`

	// Flagsets contains the cache refresh dates for each flagset when using multiple flagsets.
	// The format is {"flagset name": "2022-06-13T11:22:55.941628+02:00"}
	Flagsets map[string]time.Time `example:"{\"default\":\"2022-06-13T11:22:55.941628+02:00\"}" json:"flagsets,omitempty"` //nolint:lll // struct tags (json, yaml, toml, jsonschema, mapstructure) can not be wrapped

	// Config is the state of the config v2 runtime: generation, reloads, flag sets and objects.
	Config *ConfigInfo `json:"config,omitempty"`
}
