package bootstrap

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// nameRE is the rule for every name: connections, exporters, notifiers and listeners.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

var (
	envNameRE   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	tableNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

type validator struct {
	pos  positions
	opts LoadOptions
	cfg  *Config
	errs []error
}

func (v *validator) fail(path string, sentinel error) {
	v.errs = append(v.errs, &Error{Path: path, Line: v.pos.lineOf(path), Err: sentinel})
}

func (v *validator) failf(path string, sentinel error, format string, args ...any) {
	v.errs = append(v.errs, &Error{
		Path: path, Line: v.pos.lineOf(path), Err: sentinel, Detail: fmt.Sprintf(format, args...),
	})
}

func (c *Config) validate(pos positions, opts LoadOptions) error {
	check := &validator{pos: pos, opts: opts, cfg: c}

	check.server()
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

func (v *validator) secret(path string, s Secret) {
	switch {
	case s.kind == secretInline && !v.opts.AllowInlineSecrets:
		v.fail(path, ErrInlineSecret)
	case s.kind == secretFromEnv && !envNameRE.MatchString(s.value):
		v.failf(path+".fromEnv", ErrInvalidSecret, "not a valid environment variable name")
	}
}

func (v *validator) server() {
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

		for j, proto := range listener.Protocols {
			protoPath := base + ".protocols[" + strconv.Itoa(j) + "]"

			if proto == "h2" || proto == "tls" {
				v.failf(protoPath, ErrReservedKey, "%q is reserved for a later release", proto)

				continue
			}

			v.oneOf(protoPath, proto, ProtocolHTTP1, ProtocolH2C)
		}
	}

	if !api {
		v.failf("server.listeners", ErrMissingValue, "at least one listener has role api")
	}
}

func (v *validator) address(path, address string, idx int, seen map[string]int) {
	_, port, err := net.SplitHostPort(address)

	n, perr := strconv.Atoi(port)
	if err != nil || perr != nil || n < 0 || n > 65535 {
		v.failf(path, ErrInvalidValue, "expected host:port such as :1031")

		return
	}

	if first, dup := seen[address]; dup {
		v.failf(path, ErrDuplicateName, "same address as server.listeners[%d]", first)
	}

	seen[address] = idx
}

func (v *validator) observability() {
	o := v.cfg.Observability
	v.oneOf("observability.logging.level", o.Logging.Level, "debug", "info", "warn", "error")
	v.oneOf("observability.logging.format", o.Logging.Format, "json", "text")
	v.oneOf("observability.tracing.sampler", o.Tracing.Sampler, "always_on", "always_off", "traceidratio",
		"parentbased_always_on", "parentbased_always_off", "parentbased_traceidratio")
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

	awsCfg := conn.aws()
	v.credentials(path+".credentials", awsCfg.Credentials)

	if *awsCfg.Retry.MaxAttempts < 1 || *awsCfg.Retry.MaxAttempts > 20 {
		v.failf(path+".retry.maxAttempts", ErrInvalidValue, "expected an integer between 1 and 20")
	}

	if awsCfg.Endpoint != "" {
		parsed, err := url.Parse(awsCfg.Endpoint)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			v.failf(path+".endpoint", ErrInvalidValue, "expected an http or https URL")
		}
	}

	if s := conn.S3; s != nil {
		v.jitter(path+".poll.jitter", *s.Poll.Jitter)
		v.oneOf(path+".requestChecksumCalculation", s.RequestChecksumCalculation, ChecksumWhenSupported,
			ChecksumWhenRequired)
		v.oneOf(path+".responseChecksumValidation", s.ResponseChecksumValidation, ChecksumWhenSupported,
			ChecksumWhenRequired)
	}
}

func (v *validator) jitter(path string, j float64) {
	if j < 0 || j > 1 {
		v.failf(path, ErrInvalidValue, "expected a number between 0 and 1")
	}
}

func (v *validator) credentials(path string, cred *Credentials) {
	v.secret(path+".accessKeyId", cred.AccessKeyID)
	v.secret(path+".secretAccessKey", cred.SecretAccessKey)
	v.secret(path+".sessionToken", cred.SessionToken)

	var allowed, required []string

	switch cred.Type {
	case CredentialsChain:
	case CredentialsStatic:
		allowed = []string{"accessKeyId", "secretAccessKey", "sessionToken"}
		required = []string{"accessKeyId", "secretAccessKey"}
	case CredentialsAssumeRole:
		allowed = []string{keyRoleARN, "sessionName", "externalId"}
		required = []string{keyRoleARN}
	case CredentialsWebIdentity:
		allowed = []string{keyRoleARN, "tokenFile", "sessionName"}
		required = []string{keyRoleARN, "tokenFile"}
	default:
		v.failf(path+".type", ErrInvalidValue, "allowed values: chain, static, assumeRole, webIdentity")

		return
	}

	v.allowed(path, reflect.ValueOf(*cred), append([]string{"type"}, allowed...), required)
}

// allowed reports the set keys of a struct that are not in the allowed list, and the required keys not set.
func (v *validator) allowed(path string, strct reflect.Value, allowed, required []string) {
	set := map[string]bool{}

	for i := range strct.NumField() {
		name, _, _ := strings.Cut(strct.Type().Field(i).Tag.Get("yaml"), ",")
		if isSet(strct.Field(i)) {
			set[name] = true

			if !slices.Contains(allowed, name) {
				v.failf(path+"."+name, ErrNotAllowed, "not valid for %s", kindOf(strct))
			}
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
