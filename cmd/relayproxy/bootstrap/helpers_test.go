package bootstrap_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
)

const (
	noReader         = "no reader"
	levelPath        = "observability.logging.level"
	levelLoud        = "loud"
	opsAddr          = "127.0.0.1:9090"
	defMaxManifest   = "Manifest"
	endpointURLKey   = "endpointUrl"
	typeKey          = "type"
	webhookURLKey    = "webhookUrl"
	v1Doc            = "version: 1\n"
	fmtPlus          = "%+v"
	fmtGo            = "%#v"
	defaultAPI       = ":1031"
	etcA             = "/etc/a.yaml"
	logFormatDefault = "json"
	v2Doc            = "version: 2\n"
	pathCacheType    = "connections.cache.type"
	formatText       = "text"
	levelDebug       = "debug"
	levelError       = "error"
	addr8080         = ":8080"
	etcBad           = "/etc/bad.yaml"
	envKeyJSON       = "env.json"
)

var errNoFile = errors.New("file does not exist")

// envMap is an injected environment.
type envMap map[string]string

func (e envMap) lookup(name string) (string, bool) {
	v, ok := e[name]

	return v, ok
}

// fileMap is an injected file system.
type fileMap map[string]string

func (f fileMap) read(name string) ([]byte, error) {
	if content, ok := f[name]; ok {
		return []byte(content), nil
	}

	return nil, errNoFile
}

func options(env envMap, files fileMap, inline bool) bootstrap.LoadOptions {
	return bootstrap.LoadOptions{LookupEnv: env.lookup, ReadFile: files.read, AllowInlineSecrets: inline}
}

// pathErrors flattens a joined error into its path-addressed errors.
func pathErrors(err error) []*bootstrap.Error {
	var out []*bootstrap.Error

	var walk func(error)

	walk = func(current error) {
		if j, ok := current.(interface{ Unwrap() []error }); ok {
			for _, c := range j.Unwrap() {
				walk(c)
			}

			return
		}

		if pe, ok := errors.AsType[*bootstrap.Error](current); ok {
			out = append(out, pe)
		}
	}

	walk(err)

	return out
}

// findError returns the error of the list that wraps the sentinel and sits at the path.
func findError(t *testing.T, err, sentinel error, path string) *bootstrap.Error {
	t.Helper()

	for _, e := range pathErrors(err) {
		if errors.Is(e, sentinel) && e.Path == path {
			return e
		}
	}

	require.Failf(t, "error not found", "want %v at %q in: %v", sentinel, path, err)

	return nil
}

const (
	tcKafka       = "kafka"
	tcConnections = "connections"
	tcAdminkeys   = "adminKeys"
	tcAccesskeyid = "accessKeyId"
	tcExporters   = "exporters"
	tcAccountkey  = "accountKey"
	tcHeaders     = "headers"
	tcRolearn     = "roleArn"
	tcBucket      = "bucket"
	tcMeta        = "meta"
	tcPath        = "path"
	tcSecret      = "secret"
	tcExternalid  = "externalId"
	tcTablename   = "tableName"
	tcKind        = "kind"
	tcConnection  = "connection"
)

const (
	tcAuth              = "auth"
	tcSecretaccesskey   = "secretAccessKey"
	tcCredentials       = "credentials"
	tcNotifiers         = "notifiers"
	tcGooglecredentials = "googleCredentials"
)

const tcSessiontoken = "sessionToken"
