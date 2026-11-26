package bootstrap

import (
	"fmt"
	"io"

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

// pick returns the flag if set, else the environment variable.
func (o LoadOptions) pick(flag, name string) string {
	if flag != "" {
		return flag
	}

	return o.env(name)
}

// Load reads the bootstrap file named by the --config flag or SEMAPHORE_CONFIG, then parses, overrides, defaults
// and validates it.
func Load(overrides *Overrides, opts LoadOptions) (*Config, error) {
	overrides = orEmpty(overrides)

	path := opts.pick(overrides.Config, EnvConfig)
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

	cfg.applyOverrides(overrides, opts)
	cfg.applyDefaults()

	err = cfg.validate(pos, opts)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) applyOverrides(overrides *Overrides, opts LoadOptions) {
	if v := opts.pick(overrides.LogLevel, EnvLogLevel); v != "" {
		c.Observability.Logging.Level = v
	}

	if v := opts.pick(overrides.LogFormat, EnvLogFormat); v != "" {
		c.Observability.Logging.Format = v
	}

	if v := opts.pick(overrides.Listen, EnvListen); v != "" {
		c.setListener(RoleAPI, v)
	}

	if v := opts.pick(overrides.OpsListen, EnvOpsListen); v != "" {
		c.setListener(RoleOps, v)
	}

	if c.ControlPlane != nil {
		if v := opts.pick(overrides.ManifestKey, EnvManifestKey); v != "" {
			c.ControlPlane.Manifest.Key = v
		}

		if v := opts.pick(overrides.StateDir, EnvStateDir); v != "" {
			c.ControlPlane.StateDir = v
		}
	}
}

// setListener sets the address of the first listener with the role, and creates it when there is none.
func (c *Config) setListener(role, address string) {
	for i := range c.Server.Listeners {
		l := &c.Server.Listeners[i]
		if l.Role == role || (l.Role == "" && role == RoleAPI) {
			l.Address = address

			return
		}
	}

	if len(c.Server.Listeners) == 0 && role == RoleOps {
		// the default api listener is not lost when only the ops address is set
		c.Server.Listeners = append(
			c.Server.Listeners,
			Listener{Name: RoleAPI, Address: DefaultAPIAddress, Role: RoleAPI},
		)
	}

	c.Server.Listeners = append(c.Server.Listeners, Listener{Name: role, Address: address, Role: role})
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
