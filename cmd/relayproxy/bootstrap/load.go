package bootstrap

import (
	"fmt"
	"io"
	"slices"
	"strconv"

	"gopkg.in/yaml.v3"
)

// printIndent is the indentation of the effective print.
const printIndent = 2

// The environment variables the loader reads. No other variable is read.
const (
	EnvConfig      = "SEMAPHORE_CONFIG"
	EnvLogLevel    = "SEMAPHORE_LOG_LEVEL"
	EnvLogFormat   = "SEMAPHORE_LOG_FORMAT"
	EnvListen      = "SEMAPHORE_LISTEN"
	EnvOpsListen   = "SEMAPHORE_OPS_LISTEN"
	EnvManifestKey = "SEMAPHORE_MANIFEST_KEY"
	EnvStateDir    = "SEMAPHORE_STATE_DIR"
)

// LoadOptions injects what the loader needs from the outside world.
type LoadOptions struct {
	// LookupEnv reads an environment variable, like [os.LookupEnv]. A nil function means an empty environment.
	LookupEnv func(string) (string, bool)
	// ReadFile reads a file, like [os.ReadFile]. It is required by [Load] and by fromFile secrets.
	ReadFile func(string) ([]byte, error)
	// AllowInlineSecrets accepts plain strings for secrets. Development only.
	AllowInlineSecrets bool
	// SkipRelayChecks leaves out the checks that only starting the relay needs: the polling bounds of the manifest
	// reader. A tool that reads the file for another purpose (publish) sets it; the relay and `config validate`
	// do not.
	SkipRelayChecks bool
}

// Overrides are the values of the command-line flags. A flag wins over the environment, which wins over the file.
// An empty field means the flag is not set. A nil *Overrides is the same as an empty one.
type Overrides struct {
	Config      string
	LogLevel    string
	LogFormat   string
	Listen      string
	OpsListen   string
	ManifestKey string
	StateDir    string
}

// orEmpty turns a nil Overrides into an empty one.
func orEmpty(overrides *Overrides) *Overrides {
	if overrides == nil {
		return &Overrides{}
	}

	return overrides
}

func (o LoadOptions) env(name string) string {
	if o.LookupEnv == nil {
		return ""
	}

	v, _ := o.LookupEnv(name)

	return v
}

// Load reads the bootstrap file named by the --config flag or SEMAPHORE_CONFIG, then parses, overrides, defaults
// and validates it.
func Load(overrides *Overrides, opts LoadOptions) (*Config, error) {
	overrides = orEmpty(overrides)

	path, _ := opts.pickSource(overrides.Config, source{flag: "--config", env: EnvConfig})
	if path == "" {
		return nil, ErrNoConfigPath
	}

	if opts.ReadFile == nil {
		return nil, ErrNoReader
	}

	data, err := opts.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read bootstrap file %s: %w", path, err)
	}

	return Parse(data, overrides, opts)
}

// Parse decodes strictly, applies the overrides and the defaults, and validates.
func Parse(data []byte, overrides *Overrides, opts LoadOptions) (*Config, error) {
	overrides = orEmpty(overrides)

	cfg, pos, err := decodeStrict(data)
	if err != nil {
		return nil, err
	}

	sources := cfg.applyOverrides(overrides, opts)
	cfg.applyDefaults()

	err = cfg.validate(pos, sources, opts)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// source says where an override comes from, for the error messages.
type source struct {
	flag string
	env  string
}

// pickSource returns the value of the flag if set, else of the environment variable, with the name of its source.
func (o LoadOptions) pickSource(flagValue string, src source) (string, string) {
	if flagValue != "" {
		return flagValue, "flag " + src.flag
	}

	if v := o.env(src.env); v != "" {
		return v, "environment variable " + src.env
	}

	return "", ""
}

// applyOverrides applies the flags and variables and returns the source of every overridden path.
func (c *Config) applyOverrides(overrides *Overrides, opts LoadOptions) map[string]string {
	sources := map[string]string{}

	if v, from := opts.pickSource(overrides.LogLevel, source{flag: "--log-level", env: EnvLogLevel}); v != "" {
		c.Observability.Logging.Level = v
		sources["observability.logging.level"] = from
	}

	if v, from := opts.pickSource(overrides.LogFormat, source{flag: "--log-format", env: EnvLogFormat}); v != "" {
		c.Observability.Logging.Format = v
		sources["observability.logging.format"] = from
	}

	if v, from := opts.pickSource(overrides.Listen, source{flag: "--listen", env: EnvListen}); v != "" {
		sources["server.listeners["+strconv.Itoa(c.setListener(RoleAPI, v))+"].address"] = from
	}

	if v, from := opts.pickSource(overrides.OpsListen, source{flag: "--ops-listen", env: EnvOpsListen}); v != "" {
		sources["server.listeners["+strconv.Itoa(c.setListener(RoleOps, v))+"].address"] = from
	}

	if c.ControlPlane != nil {
		manifestKey := source{flag: "--manifest-key", env: EnvManifestKey}
		if v, from := opts.pickSource(overrides.ManifestKey, manifestKey); v != "" {
			c.ControlPlane.Manifest.Key = v
			sources["controlPlane.manifest.key"] = from
		}

		if v, from := opts.pickSource(overrides.StateDir, source{flag: "--state-dir", env: EnvStateDir}); v != "" {
			c.ControlPlane.StateDir = v
			sources["controlPlane.stateDir"] = from
		}
	}

	return sources
}

// setListener sets the address of the first listener with the role, or adds a listener with a unique name when
// there is none. It returns the index of the listener.
func (c *Config) setListener(role, address string) int {
	for i := range c.Server.Listeners {
		listener := &c.Server.Listeners[i]
		if listener.Role == role || (listener.Role == "" && role == RoleAPI) {
			listener.Address = address

			return i
		}
	}

	if len(c.Server.Listeners) == 0 && role == RoleOps {
		// the default api listener is not lost when only the ops address is set
		c.Server.Listeners = append(
			c.Server.Listeners,
			Listener{Name: RoleAPI, Address: DefaultAPIAddress, Role: RoleAPI},
		)
	}

	name := role

	for n := 2; c.hasListener(name); n++ {
		name = role + "-" + strconv.Itoa(n)
	}

	c.Server.Listeners = append(c.Server.Listeners, Listener{Name: name, Address: address, Role: role})

	return len(c.Server.Listeners) - 1
}

func (c *Config) hasListener(name string) bool {
	return slices.ContainsFunc(c.Server.Listeners, func(l Listener) bool { return l.Name == name })
}

// Print writes the effective configuration as YAML: defaults and overrides applied, secrets and admin keys
// redacted.
func (c *Config) Print(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(printIndent)

	err := enc.Encode(c)
	if err != nil {
		return fmt.Errorf("print effective config: %w", err)
	}

	err = enc.Close()
	if err != nil {
		return fmt.Errorf("print effective config: %w", err)
	}

	return nil
}
