package bootstrap_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const settingsBase = "version: 2\nconnections:\n  c: {type: file, path: /x}\ncontrolPlane:\n  connection: c\n" +
	"  manifest: {key: m.yaml}\n"

const opsListeners = "server:\n  listeners:\n    - {name: api, address: \":1031\"}\n" +
	"    - {name: ops, address: \":1032\", role: ops}\n"

// The settings that returned with the relay: the file is strict, the errors name the path, and the defaults are the
// ones of version 1.
func TestServerSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		doc           string
		wantErr       string
		wantStreamURL string
		wantAnonymous string
		wantDelay     int
		wantJSON      int64
		wantYAML      int64
		wantTOML      int64
		wantPprof     bool
		wantHide      bool
	}{
		{
			name: "defaults stay unset: the endpoint applies 1MiB, 64KiB and 64KiB", doc: settingsBase,
		},
		{
			name: "everything set", wantPprof: true, wantHide: true,
			doc: settingsBase + "auth:\n  anonymousFlagSet: public\n" + opsListeners +
				"  hideBanner: true\n  ops: {pprof: {enabled: true}}\n" +
				"  ofrepEventStream: {baseUrl: \"https://flags.example.com/base\", inactivityDelaySec: 30}\n" +
				"  validateFlags: {maxJsonBodySize: 2MiB, maxYamlBodySize: 100KiB, maxTomlBodySize: 1500B}\n",
			wantStreamURL: "https://flags.example.com/base", wantDelay: 30, wantAnonymous: "public",
			wantJSON: 2 << 20, wantYAML: 100 << 10, wantTOML: 1500,
		},
		{
			name: "a size of zero is the default of the endpoint", doc: settingsBase +
				"server:\n  validateFlags: {maxJsonBodySize: 0B}\n",
		},
		{
			name: "pprof needs an ops listener",
			doc:  settingsBase + "server:\n  ops: {pprof: {enabled: true}}\n",
			wantErr: "server.ops.pprof.enabled (line 8): invalid value: needs a listener with role ops, " +
				"pprof is never served on the api listener",
		},
		{
			name:    "an unknown key under ops",
			doc:     settingsBase + "server:\n  ops: {pprof: {enable: true}}\n",
			wantErr: bootstrap.ErrUnknownKey.Error(),
		},
		{
			name:    "an anonymous flag set that is not a name",
			doc:     settingsBase + "auth:\n  anonymousFlagSet: Not A Name\n",
			wantErr: "auth.anonymousFlagSet (line 8): invalid name",
		},
		{
			name:    "a stream URL that is not http",
			doc:     settingsBase + "server:\n  ofrepEventStream: {baseUrl: \"ftp://flags.example.com\"}\n",
			wantErr: "server.ofrepEventStream.baseUrl (line 8): invalid value: expected an http or https URL",
		},
		{
			name:    "a stream URL with credentials",
			doc:     settingsBase + "server:\n  ofrepEventStream: {baseUrl: \"https://user:pw@flags.example.com\"}\n",
			wantErr: "credentials in the URL are not allowed, use a secret reference",
		},
		{
			name:    "a stream URL with a query",
			doc:     settingsBase + "server:\n  ofrepEventStream: {baseUrl: \"https://flags.example.com/?a=b\"}\n",
			wantErr: "a query or a fragment is not allowed",
		},
		{
			name:    "a negative inactivity delay",
			doc:     settingsBase + "server:\n  ofrepEventStream: {inactivityDelaySec: -1}\n",
			wantErr: "server.ofrepEventStream.inactivityDelaySec (line 8): invalid value: expected zero or more seconds",
		},
		{
			name:    "a size that is a bare number",
			doc:     settingsBase + "server:\n  validateFlags: {maxJsonBodySize: 100}\n",
			wantErr: "invalid size: write it as a string such as \"64KiB\"",
		},
		{
			name:    "a fraction of a byte",
			doc:     settingsBase + "server:\n  validateFlags: {maxJsonBodySize: 0.5B}\n",
			wantErr: "invalid size: expected a size such as \"64KiB\", with a fraction only above the byte",
		},
		{
			name:    "a size below one byte",
			doc:     settingsBase + "server:\n  validateFlags: {maxJsonBodySize: 0.0001KiB}\n",
			wantErr: "invalid size: the size is below one byte",
		},
		{
			name:    "a size that is not a size",
			doc:     settingsBase + "server:\n  validateFlags: {maxTomlBodySize: lots}\n",
			wantErr: "invalid size: expected a size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := bootstrap.Parse([]byte(tt.doc), nil, options(nil, nil, false))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.NotContains(t, err.Error(), "pw@", "a URL with credentials is never in an error")

				return
			}

			require.NoError(t, err)

			assert.Equal(t, tt.wantPprof, cfg.Server.Ops.Pprof.Enabled)
			assert.Equal(t, tt.wantHide, cfg.Server.HideBanner)
			assert.Equal(t, tt.wantStreamURL, cfg.Server.OFREPEventStream.BaseURL)
			assert.Equal(t, tt.wantDelay, cfg.Server.OFREPEventStream.InactivityDelaySec)
			assert.Equal(t, tt.wantAnonymous, cfg.Auth.AnonymousFlagSet)
			assert.Equal(t, tt.wantJSON, sizeOf(cfg.Server.ValidateFlags.MaxJSONBodySize))
			assert.Equal(t, tt.wantYAML, sizeOf(cfg.Server.ValidateFlags.MaxYAMLBodySize))
			assert.Equal(t, tt.wantTOML, sizeOf(cfg.Server.ValidateFlags.MaxTOMLBodySize))
		})
	}
}

// The effective print writes the sizes in the form the file reads, and what it prints loads again.
func TestValidateFlagsPrintRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		size      string
		wantPrint string
	}{
		{name: "mebibytes", size: "3MiB", wantPrint: "maxJsonBodySize: 3MiB"},
		{name: "kibibytes", size: "96KiB", wantPrint: "maxJsonBodySize: 96KiB"},
		{name: "bytes", size: "1000B", wantPrint: "maxJsonBodySize: 1000B"},
		{name: "a decimal unit", size: "1MB", wantPrint: "maxJsonBodySize: 1000000B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			doc := settingsBase + "server:\n  validateFlags: {maxJsonBodySize: " + tt.size + "}\n"

			cfg, err := bootstrap.Parse([]byte(doc), nil, options(nil, nil, false))
			require.NoError(t, err)

			var printed bytes.Buffer
			require.NoError(t, cfg.Print(&printed))
			assert.Contains(t, printed.String(), tt.wantPrint)

			again, err := bootstrap.Parse(printed.Bytes(), nil, options(nil, nil, false))
			require.NoError(t, err)
			assert.Equal(t, *cfg.Server.ValidateFlags.MaxJSONBodySize, *again.Server.ValidateFlags.MaxJSONBodySize)
		})
	}
}

// sizeOf is the size in bytes of a limit, 0 when the file does not set it.
func sizeOf(size *bootstrap.ByteSize) int64 {
	if size == nil {
		return 0
	}

	return int64(*size)
}
