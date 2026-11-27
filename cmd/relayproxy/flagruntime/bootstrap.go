package flagruntime

import (
	"fmt"
	"reflect"
	"slices"

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

// pendingRestart lists the settings of cfg that differ from the ones the process started with. Only names are
// listed: a value may be a secret. Auth.AdminKeys is not compared: it applies live.
func pendingRestart(base, cfg *bootstrap.Config) []string {
	var out []string

	add := func(changed bool, name string) {
		if changed {
			out = append(out, name)
		}
	}

	add(base.Version != cfg.Version, SettingVersion)
	add(!reflect.DeepEqual(base.Server.Listeners, cfg.Server.Listeners), SettingServerListeners)
	add(base.Server.UnixSocketPath != cfg.Server.UnixSocketPath, SettingServerUnixSocket)
	add(!reflect.DeepEqual(base.Server.Shutdown, cfg.Server.Shutdown), SettingServerShutdown)
	add(!reflect.DeepEqual(base.Connections, cfg.Connections), SettingConnections)

	switch {
	case (base.ControlPlane == nil) != (cfg.ControlPlane == nil):
		out = append(out, SettingControlPlane)
	case base.ControlPlane != nil:
		add(base.ControlPlane.Connection != cfg.ControlPlane.Connection, SettingControlPlaneConn)
		add(base.ControlPlane.StateDir != cfg.ControlPlane.StateDir, SettingControlPlaneState)
		add(!reflect.DeepEqual(base.ControlPlane.Manifest, cfg.ControlPlane.Manifest), SettingControlPlaneMan)
	}

	add(!reflect.DeepEqual(base.Exporters, cfg.Exporters), SettingExporters)
	add(!reflect.DeepEqual(base.Notifiers, cfg.Notifiers), SettingNotifiers)
	add(!reflect.DeepEqual(base.Observability, cfg.Observability), SettingObservability)
	add(!reflect.DeepEqual(base.Evaluation, cfg.Evaluation), SettingEvaluation)

	slices.Sort(out)

	return out
}
