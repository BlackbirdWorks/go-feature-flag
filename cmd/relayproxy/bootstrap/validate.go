package bootstrap

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// nameRE is the rule for every name: connections, exporters, notifiers and listeners.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

var (
	envNameRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

type validator struct {
	pos     positions
	sources map[string]string
	opts    LoadOptions
	cfg     *Config
	errs    []error
}

func (v *validator) fail(path string, sentinel error) {
	v.failf(path, sentinel, "")
}

// failf records an error. A value that comes from a flag or a variable names its source and has no file line.
func (v *validator) failf(path string, sentinel error, format string, args ...any) {
	detail := fmt.Sprintf(format, args...)

	if source, ok := v.sources[path]; ok {
		if detail != "" {
			detail = ": " + detail
		}

		v.errs = append(v.errs, &Error{Path: path, Err: sentinel, Detail: "set by " + source + detail})

		return
	}

	v.errs = append(v.errs, &Error{Path: path, Line: v.pos.lineOf(path), Err: sentinel, Detail: detail})
}

func (c *Config) validate(pos positions, sources map[string]string, opts LoadOptions) error {
	check := &validator{pos: pos, sources: sources, opts: opts, cfg: c}

	check.server()
	check.serverSettings()
	check.anonymousFlagSet()
	check.observability()
	check.adminKeys()

	for _, name := range sortedKeys(c.Connections) {
		check.name("connections."+name, name)
		check.connection("connections."+name, c.Connections[name])
	}

	for _, name := range sortedKeys(c.Exporters) {
		check.name("exporters."+name, name)
		exporter := c.Exporters[name]
		check.exporter("exporters."+name, &exporter)
	}

	for _, name := range sortedKeys(c.Notifiers) {
		check.name("notifiers."+name, name)
		notifier := c.Notifiers[name]
		check.notifier("notifiers."+name, &notifier)
	}

	check.controlPlane()
	check.segments()

	return errors.Join(check.errs...)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

func (v *validator) name(path, name string) {
	switch {
	case name == "default":
		v.failf(path, ErrReservedName, "%q is reserved", name)
	case !nameRE.MatchString(name):
		v.failf(path, ErrInvalidName, "names match %s", nameRE.String())
	}
}

func (v *validator) oneOf(path, value string, allowed ...string) {
	if !slices.Contains(allowed, value) {
		v.failf(path, ErrInvalidValue, "allowed values: %s", strings.Join(allowed, ", "))
	}
}

func (v *validator) secret(path string, secret Secret) {
	switch {
	case secret.kind == secretInline && !v.opts.AllowInlineSecrets:
		v.fail(path, ErrInlineSecret)
	case secret.kind == secretInline && secret.value == "":
		v.failf(path, ErrMissingValue, "empty secret")
	case secret.kind == secretFromEnv && !envNameRE.MatchString(secret.value):
		v.failf(path+".fromEnv", ErrInvalidSecret, "not a valid environment variable name")
	}
}

func (v *validator) shutdown() {
	for _, path := range []string{pathDrainDelay, pathTimeout} {
		if detail, bad := v.cfg.badEnv[path]; bad {
			v.failf(path, ErrInvalidDuration, "%s", detail)
		}
	}

	shutdown := v.cfg.Server.Shutdown
	if shutdown.DrainDelay != nil && time.Duration(*shutdown.DrainDelay) > MaxShutdownDuration {
		v.failf(pathDrainDelay, ErrInvalidValue, "expected at most %s", MaxShutdownDuration)
	}

	if shutdown.Timeout != nil && time.Duration(*shutdown.Timeout) > MaxShutdownDuration {
		v.failf(pathTimeout, ErrInvalidValue, "expected at most %s", MaxShutdownDuration)
	}
}

func (v *validator) server() {
	v.shutdown()

	s := v.cfg.Server
	names := map[string]int{}
	addrs := map[string]int{}
	api := false

	for i, listener := range s.Listeners {
		base := "server.listeners[" + strconv.Itoa(i) + "]"

		if listener.Name == "" {
			v.fail(base+".name", ErrMissingValue)
		} else {
			v.name(base+".name", listener.Name)

			if first, dup := names[listener.Name]; dup {
				v.failf(base+".name", ErrDuplicateName, "same name as server.listeners[%d]", first)
			}

			names[listener.Name] = i
		}

		v.address(base+".address", listener.Address, i, addrs)
		v.oneOf(base+".role", listener.Role, RoleAPI, RoleOps)

		api = api || listener.Role == RoleAPI

		for index, proto := range listener.Protocols {
			protoPath := base + ".protocols[" + strconv.Itoa(index) + "]"

			if proto == "h2" || proto == "tls" {
				v.failf(protoPath, ErrReservedKey, "%q is reserved for a later release", proto)

				continue
			}

			v.oneOf(protoPath, proto, ProtocolHTTP1, ProtocolH2C)

			if first := slices.Index(listener.Protocols, proto); first != index {
				v.failf(protoPath, ErrDuplicateName, "same protocol as protocols[%d]", first)
			}
		}
	}

	if !api {
		v.failf("server.listeners", ErrMissingValue, "at least one listener has role api")
	}

	if !v.opts.SkipRelayChecks {
		v.serveLayout()
	}
}

// serveLayout refuses the listener layouts that the relay can not serve yet: a unix socket, several listeners of a
// role and a port 0.
func (v *validator) serveLayout() {
	server := v.cfg.Server

	if server.UnixSocketPath != "" {
		v.failf("server.unixSocketPath", ErrReservedKey, "the relay does not serve a unix socket yet")
	}

	roles := map[string]int{}

	for i, listener := range server.Listeners {
		base := "server.listeners[" + strconv.Itoa(i) + "]"

		_, port, err := net.SplitHostPort(listener.Address)
		if err != nil {
			continue // address() reported it
		}

		n, convErr := strconv.Atoi(port)
		if convErr == nil && n == 0 {
			v.failf(base+".address", ErrInvalidValue, "the port must be between 1 and 65535")
		}

		if first, dup := roles[listener.Role]; dup {
			v.failf(
				base+".role",
				ErrInvalidValue,
				"the relay serves one %s listener, server.listeners[%d] is one",
				listener.Role,
				first,
			)

			continue
		}

		roles[listener.Role] = i
	}

	v.overlap(roles)
}

// overlap refuses an api and an ops listener that can not both bind: the same port, with a wildcard host on a side.
// The check is conservative: a "::" listener is taken as covering the IPv4 hosts too (dual stack).
func (v *validator) overlap(roles map[string]int) {
	apiIdx, hasAPI := roles[RoleAPI]
	opsIdx, hasOps := roles[RoleOps]

	if !hasAPI || !hasOps {
		return
	}

	listeners := v.cfg.Server.Listeners
	apiHost, apiPort, apiErr := net.SplitHostPort(listeners[apiIdx].Address)
	opsHost, opsPort, opsErr := net.SplitHostPort(listeners[opsIdx].Address)

	if apiErr != nil || opsErr != nil || apiPort != opsPort || listeners[apiIdx].Address == listeners[opsIdx].Address {
		return // the duplicate address is reported by address()
	}

	if hostsOverlap(apiHost, opsHost) {
		v.failf("server.listeners["+strconv.Itoa(opsIdx)+"].address", ErrInvalidValue,
			"the port is also bound by server.listeners[%d] and one of the two hosts is a wildcard: both can not be "+
				"bound (checked conservatively, \"::\" covers IPv4 too)", apiIdx)
	}
}

// hostsOverlap reports whether two hosts of one port can not both be bound because one is a wildcard that covers the
// other: "" and "::" cover every host, "0.0.0.0" covers the IPv4 addresses and the names (which may resolve to one).
func hostsOverlap(left, right string) bool {
	return covers(left, right) || covers(right, left)
}

func covers(wide, other string) bool {
	switch wide {
	case "", "::":
		return true
	case "0.0.0.0":
		addr, err := netip.ParseAddr(other)

		return err != nil || addr.Is4() || addr.Is4In6()
	}

	return false
}

func (v *validator) address(path, address string, idx int, seen map[string]int) {
	_, port, err := net.SplitHostPort(address)

	n, perr := strconv.Atoi(port)
	if err != nil || perr != nil || n < 0 || n > maxPort {
		v.failf(path, ErrInvalidValue, "expected host:port such as :1031")

		return
	}

	if first, dup := seen[address]; dup {
		v.failf(path, ErrDuplicateName, "same address as server.listeners[%d]", first)
	}

	seen[address] = idx
}

func (v *validator) observability() {
	obs := v.cfg.Observability
	v.oneOf("observability.logging.level", obs.Logging.Level, "debug", "info", "warn", "error")
	v.oneOf("observability.logging.format", obs.Logging.Format, "json", "text")
	v.oneOf("observability.tracing.sampler", obs.Tracing.Sampler, "always_on", "always_off", "traceidratio",
		"parentbased_always_on", "parentbased_always_off", "parentbased_traceidratio")

	if obs.Tracing.OTLPEndpoint != "" {
		v.endpointOrHostPort("observability.tracing.otlpEndpoint", obs.Tracing.OTLPEndpoint)
	}
}

// httpURL checks an http or https URL. It never accepts credentials in the URL, because the URL is printed.
func (v *validator) httpURL(path, value string) {
	if value == "" {
		return
	}

	parsed, err := url.Parse(value)

	switch {
	case err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "":
		v.failf(path, ErrInvalidValue, "expected an http or https URL")
	case parsed.User != nil:
		v.failf(path, ErrInvalidValue, "credentials in the URL are not allowed, use a secret reference")
	}
}

// hostPort checks a host:port address without user information.
func (v *validator) hostPort(path, value string) {
	host, port, err := net.SplitHostPort(value)
	portNumber, portErr := strconv.Atoi(port)
	signed := strings.IndexFunc(port, func(r rune) bool { return r < '0' || r > '9' }) >= 0

	switch {
	case strings.Contains(value, "@"):
		v.failf(path, ErrInvalidValue, "credentials in the address are not allowed")
	case err != nil || host == "" || portErr != nil || signed || portNumber < 1 || portNumber > maxPort:
		v.failf(path, ErrInvalidValue, "expected host:port")
	}
}

func (v *validator) endpointOrHostPort(path, value string) {
	if strings.Contains(value, "://") {
		v.httpURL(path, value)

		return
	}

	v.hostPort(path, value)
}

func (v *validator) adminKeys() {
	seen := map[string]int{}

	for i, key := range v.cfg.Auth.AdminKeys {
		base := "auth.adminKeys[" + strconv.Itoa(i) + "]"

		if !key.IsSet() || (key.kind == secretInline && key.value == "") {
			v.failf(base, ErrMissingValue, "empty key")

			continue
		}

		if v.secretRefInvalid(base, key.Secret) {
			continue
		}

		id := strconv.Itoa(int(key.kind)) + ":" + key.value
		if first, dup := seen[id]; dup {
			v.failf(base, ErrDuplicateKey, "same key as auth.adminKeys[%d]", first)

			continue
		}

		seen[id] = i
	}
}

// secretRefInvalid checks the reference form of a secret without the inline rule.
func (v *validator) secretRefInvalid(path string, s Secret) bool {
	if s.kind == secretFromEnv && !envNameRE.MatchString(s.value) {
		v.failf(path+".fromEnv", ErrInvalidSecret, "not a valid environment variable name")

		return true
	}

	return false
}

func (v *validator) connection(path string, conn Connection) {
	if conn.Type() == "" {
		v.fail(path+".type", ErrMissingValue)

		return
	}

	if conn.File != nil {
		v.require(path+".path", strings.TrimSpace(conn.File.Path) != "", "")

		return
	}

	if conn.Redis != nil || conn.Valkey != nil {
		v.storeConnection(path, conn)

		return
	}

	awsCfg := conn.aws()
	v.credentials(path+".credentials", awsCfg.Credentials)

	if *awsCfg.Retry.MaxAttempts < 1 || *awsCfg.Retry.MaxAttempts > 20 {
		v.failf(path+".retry.maxAttempts", ErrInvalidValue, "expected an integer between 1 and 20")
	}

	v.httpURL(path+".endpoint", awsCfg.Endpoint)

	if conn.SQS != nil {
		v.httpURL(path+".queueUrl", conn.SQS.QueueURL)
	}

	if s := conn.S3; s != nil {
		v.require(path+".region", strings.TrimSpace(s.Region) != "", "")
		v.jitter(path+".poll.jitter", *s.Poll.Jitter)
		v.oneOf(path+".requestChecksumCalculation", s.RequestChecksumCalculation, ChecksumWhenSupported,
			ChecksumWhenRequired)
		v.oneOf(path+".responseChecksumValidation", s.ResponseChecksumValidation, ChecksumWhenSupported,
			ChecksumWhenRequired)
	}
}

func (v *validator) jitter(path string, j float64) {
	if !(j >= 0 && j <= 1) {
		v.failf(path, ErrInvalidValue, "expected a number between 0 and 1")
	}
}

// credentialKeys returns the keys a credentials type allows besides keyType, and the ones it requires.
func credentialKeys(kind string) ([]string, []string, bool) {
	switch kind {
	case CredentialsChain:
		return nil, nil, true
	case CredentialsStatic:
		return []string{keyAccessKeyID, keySecretKey, "sessionToken"}, []string{keyAccessKeyID, keySecretKey}, true
	case CredentialsAssumeRole:
		return []string{keyRoleARN, "sessionName", "externalId"}, []string{keyRoleARN}, true
	case CredentialsWebIdentity:
		return []string{keyRoleARN, keyTokenFile, "sessionName"}, []string{keyRoleARN, keyTokenFile}, true
	}

	return nil, nil, false
}

func (v *validator) credentials(path string, cred *Credentials) {
	v.secret(path+".accessKeyId", cred.AccessKeyID)
	v.secret(path+".secretAccessKey", cred.SecretAccessKey)
	v.secret(path+".sessionToken", cred.SessionToken)
	v.secret(path+".externalId", cred.ExternalID)

	allowed, required, found := credentialKeys(cred.Type)
	if !found {
		v.failf(path+".type", ErrInvalidValue, "allowed values: chain, static, assumeRole, webIdentity")

		return
	}

	v.allowed(path, reflect.ValueOf(*cred), append([]string{keyType}, allowed...), required)
}

// allowed reports the set keys of a struct that are not in the allowed list, and the required keys not set.
func (v *validator) allowed(path string, strct reflect.Value, allowed, required []string) {
	set := map[string]bool{}

	for i := range strct.NumField() {
		name, _, _ := strings.Cut(strct.Type().Field(i).Tag.Get("yaml"), ",")
		if isSet(strct.Field(i)) {
			set[name] = true
		}

		// a key that is written is checked even when its value is a zero value
		if _, written := v.pos[path+"."+name]; written && !slices.Contains(allowed, name) {
			v.failf(path+"."+name, ErrNotAllowed, "not valid for %s", kindOf(strct))
		}
	}

	for _, r := range required {
		if !set[r] {
			v.fail(path+"."+r, ErrMissingValue)
		}
	}
}

func kindOf(strct reflect.Value) string {
	for _, f := range []string{"Kind", "Type"} {
		if field := strct.FieldByName(f); field.IsValid() && field.Kind() == reflect.String {
			return f + " " + strconv.Quote(field.String())
		}
	}

	return "this section"
}

func isSet(field reflect.Value) bool {
	if field.Kind() == reflect.Map || field.Kind() == reflect.Slice {
		return field.Len() > 0
	}

	return !field.IsZero()
}
