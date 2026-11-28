package flagruntime

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

// The settings that need a restart, as they appear in Status.PendingRestart.
const (
	SettingVersion           = "version"
	SettingServerListeners   = "server.listeners"
	SettingServerUnixSocket  = "server.unixSocketPath"
	SettingServerShutdown    = "server.shutdown"
	SettingConnections       = "connections"
	SettingControlPlane      = "controlPlane"
	SettingControlPlaneConn  = "controlPlane.connection"
	SettingControlPlaneState = "controlPlane.stateDir"
	SettingControlPlaneMan   = "controlPlane.manifest"
	SettingExporters         = "exporters"
	SettingNotifiers         = "notifiers"
	SettingObservability     = "observability"
	SettingEvaluation        = "evaluation"
	SettingSegments          = "segments"
	SettingServerOps         = "server.ops"
	SettingServerEventStream = "server.ofrepEventStream"
	SettingServerValidate    = "server.validateFlags"
	SettingServerHideBanner  = "server.hideBanner"
)

// ResolveAdminKeys reads the admin keys of a bootstrap configuration. The error names an index, never a value.
func ResolveAdminKeys(cfg *bootstrap.Config, opts bootstrap.LoadOptions) (map[string]struct{}, error) {
	keys := make(map[string]struct{}, len(cfg.Auth.AdminKeys))

	for i := range cfg.Auth.AdminKeys {
		secret, err := cfg.Auth.AdminKeys[i].Resolve(opts)
		if err != nil {
			return nil, fmt.Errorf("%w: auth.adminKeys[%d]: %w", ErrInvalidBootstrap, i, err)
		}

		if value := secret.Reveal(); value != "" {
			keys[value] = struct{}{}
		}
	}

	return keys, nil
}

// Reloadability says what a reload of the bootstrap file does with one setting.
type Reloadability struct {
	// Path is the bootstrap key path that the rule covers, with every key below it.
	Path string
	// Setting is the name in Status.PendingRestart; empty for a setting that applies live.
	Setting string
	// Live is true when a reload applies the setting in place.
	Live bool
}

// rule is a row of the reload table: what it covers and how to tell that a reload changed it.
type rule struct {
	changed func(base, cfg *bootstrap.Config) bool
	meta    Reloadability
}

func differs[T any](get func(*bootstrap.Config) T) func(base, cfg *bootstrap.Config) bool {
	return func(base, cfg *bootstrap.Config) bool { return !reflect.DeepEqual(get(base), get(cfg)) }
}

func restartRule(path, setting string, changed func(base, cfg *bootstrap.Config) bool) rule {
	return rule{meta: Reloadability{Path: path, Setting: setting, Live: false}, changed: changed}
}

// controlPlaneChanged compares a field of the control plane; a change of presence is the setting controlPlane.
func controlPlaneChanged(get func(*bootstrap.ControlPlane) any) func(base, cfg *bootstrap.Config) bool {
	return func(base, cfg *bootstrap.Config) bool {
		return base.ControlPlane != nil && cfg.ControlPlane != nil &&
			!reflect.DeepEqual(get(base.ControlPlane), get(cfg.ControlPlane))
	}
}

// rules is the one table of the reload: the bootstrap settings that apply live and the ones that need a restart. The
// engine reads it to fill Status.PendingRestart, and the documentation test reads it through Reloadabilities.
func rules() []rule {
	return []rule{
		restartRule("version", SettingVersion, differs(func(c *bootstrap.Config) int { return c.Version })),
		restartRule("server.listeners", SettingServerListeners,
			differs(func(c *bootstrap.Config) any { return c.Server.Listeners })),
		restartRule("server.unixSocketPath", SettingServerUnixSocket,
			differs(func(c *bootstrap.Config) any { return c.Server.UnixSocketPath })),
		restartRule("server.shutdown", SettingServerShutdown,
			differs(func(c *bootstrap.Config) any { return c.Server.Shutdown })),
		restartRule("server.ops", SettingServerOps, differs(func(c *bootstrap.Config) any { return c.Server.Ops })),
		restartRule("server.ofrepEventStream", SettingServerEventStream,
			differs(func(c *bootstrap.Config) any { return c.Server.OFREPEventStream })),
		restartRule("server.validateFlags", SettingServerValidate,
			differs(func(c *bootstrap.Config) any { return c.Server.ValidateFlags })),
		restartRule("server.hideBanner", SettingServerHideBanner,
			differs(func(c *bootstrap.Config) any { return c.Server.HideBanner })),
		restartRule("connections", SettingConnections, differs(func(c *bootstrap.Config) any { return c.Connections })),
		restartRule("controlPlane", SettingControlPlane, func(base, cfg *bootstrap.Config) bool {
			return (base.ControlPlane == nil) != (cfg.ControlPlane == nil)
		}),
		restartRule("controlPlane.connection", SettingControlPlaneConn,
			controlPlaneChanged(func(c *bootstrap.ControlPlane) any { return c.Connection })),
		restartRule("controlPlane.stateDir", SettingControlPlaneState,
			controlPlaneChanged(func(c *bootstrap.ControlPlane) any { return c.StateDir })),
		restartRule("controlPlane.manifest", SettingControlPlaneMan,
			controlPlaneChanged(func(c *bootstrap.ControlPlane) any { return c.Manifest })),
		restartRule("exporters", SettingExporters, differs(func(c *bootstrap.Config) any { return c.Exporters })),
		restartRule("notifiers", SettingNotifiers, differs(func(c *bootstrap.Config) any { return c.Notifiers })),
		restartRule("observability", SettingObservability,
			differs(func(c *bootstrap.Config) any { return c.Observability })),
		restartRule("evaluation", SettingEvaluation, differs(func(c *bootstrap.Config) any { return c.Evaluation })),
		restartRule("segments", SettingSegments, differs(func(c *bootstrap.Config) any { return c.Segments })),
		// The admin keys are read again at every reload, so they are never compared.
		{meta: Reloadability{Path: "auth", Setting: "", Live: true}},
	}
}

// Reloadabilities returns the reload table: one entry per setting of the bootstrap file, in the order of the engine.
func Reloadabilities() []Reloadability {
	table := rules()
	out := make([]Reloadability, len(table))

	for i := range table {
		out[i] = table[i].meta
	}

	return out
}

// ReloadOf returns the rule that covers a bootstrap key path: the longest rule path that the key equals or sits below.
func ReloadOf(path string) (Reloadability, bool) {
	var best Reloadability

	found := false

	for _, entry := range Reloadabilities() {
		covers := path == entry.Path || strings.HasPrefix(path, entry.Path+".") ||
			strings.HasPrefix(path, entry.Path+"[]")
		if covers && len(entry.Path) > len(best.Path) {
			best, found = entry, true
		}
	}

	return best, found
}

// pendingRestart lists the settings of cfg that differ from the ones the process started with. Only names are
// listed: a value may be a secret.
func pendingRestart(base, cfg *bootstrap.Config) []string {
	var out []string

	for _, entry := range rules() {
		if entry.changed != nil && entry.changed(base, cfg) {
			out = append(out, entry.meta.Setting)
		}
	}

	slices.Sort(out)

	return out
}
