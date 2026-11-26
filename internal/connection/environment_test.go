package connection_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// sdkEnvironment returns the variables the SDK reads, taken from the environment config source of the pinned
// version (config/env_config.go), plus the service endpoint variables it builds from a service name.
func sdkEnvironment() []string {
	return []string{
		"AWS_ACCESS_KEY", "AWS_ACCESS_KEY_ID", "AWS_ACCOUNT_ID", "AWS_ACCOUNT_ID_ENDPOINT_MODE",
		"AWS_AUTH_SCHEME_PREFERENCE", "AWS_CA_BUNDLE", "AWS_CONFIG_FILE", "AWS_CONTAINER_AUTHORIZATION_TOKEN",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_DEFAULT_PROFILE",
		"AWS_DEFAULT_REGION", "AWS_DEFAULTS_MODE", "AWS_DISABLE_CLOCK_SKEW_CORRECTION",
		"AWS_DISABLE_REQUEST_COMPRESSION", "AWS_EC2_METADATA_DISABLED", "AWS_EC2_METADATA_SERVICE_ENDPOINT",
		"AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE", "AWS_EC2_METADATA_V1_DISABLED", "AWS_ENABLE_ENDPOINT_DISCOVERY",
		"AWS_ENDPOINT_URL", "AWS_ENDPOINT_URL_S3", "AWS_ENDPOINT_URL_STS", "AWS_IGNORE_CONFIGURED_ENDPOINT_URLS",
		"AWS_MAX_ATTEMPTS", "AWS_PROFILE", "AWS_REGION", "AWS_REQUEST_CHECKSUM_CALCULATION",
		"AWS_REQUEST_MIN_COMPRESSION_SIZE_BYTES", "AWS_RESPONSE_CHECKSUM_VALIDATION", "AWS_RESTRICT_FILE_PERMISSIONS",
		"AWS_RETRY_MODE", "AWS_ROLE_ARN", "AWS_ROLE_SESSION_NAME", "AWS_S3_DISABLE_EXPRESS_SESSION_AUTH",
		"AWS_S3_DISABLE_MULTIREGION_ACCESS_POINTS", "AWS_S3_USE_ARN_REGION", "AWS_SDK_UA_APP_ID",
		"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_KEY", "AWS_SESSION_TOKEN", "AWS_SHARED_CREDENTIALS_FILE",
		"AWS_USE_DUALSTACK_ENDPOINT", "AWS_USE_FIPS_ENDPOINT", "AWS_WEB_IDENTITY_TOKEN_FILE",
	}
}

// hermeticAWSEnvironment gives a serial test an AWS environment that holds nothing of the machine: every
// variable the SDK reads, and every other AWS_* variable of the process, is unset for the test; the shared
// config and credentials files are empty files; the instance metadata service is disabled. The test then sets
// only what its cases need. The SDK reads the real process environment, which is why these tests cannot be
// parallel (t.Setenv); that is the documented exception to the parallel rule of this package.
func hermeticAWSEnvironment(t *testing.T) {
	t.Helper()

	names := sdkEnvironment()

	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(name, "AWS_") {
			names = append(names, name)
		}
	}

	for _, name := range names {
		t.Setenv(name, "") // registers the restore of the previous value
		require.NoError(t, os.Unsetenv(name))
	}

	dir := t.TempDir()
	config, credentials := filepath.Join(dir, "config"), filepath.Join(dir, "credentials")
	require.NoError(t, os.WriteFile(config, nil, 0o600))
	require.NoError(t, os.WriteFile(credentials, nil, 0o600))

	t.Setenv("AWS_CONFIG_FILE", config)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", credentials)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

// TestFactory_ConnectionsIgnoreTheProcessEnvironment runs each credentials type against a hostile process
// environment (region, endpoints, defaults mode, a CA bundle, and a missing profile for the types that do not
// read the chain) and checks, on the signed request, that region, endpoint and key come from the spec, and
// from the environment only for the chain. It is serial: see [hermeticAWSEnvironment].
func TestFactory_ConnectionsIgnoreTheProcessEnvironment(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("jwt"), 0o600))

	tests := []struct {
		env          map[string]string // set on top of the hostile base, for this case only
		name         string
		stsResponse  string
		wantKeyID    string // key id that signs the S3 request
		wantSTSSigns string // key id that signs the STS request, when there is one
		creds        connection.Credentials
	}{
		{
			name:      "chain uses the environment credentials only",
			creds:     connection.Credentials{},
			wantKeyID: keyChain,
		},
		{
			name: "static ignores a missing profile and the environment keys",
			env:  map[string]string{envProfile: missingProfile},
			creds: connection.Credentials{
				Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret("AKIDSPEC"),
				SecretAccessKey: connection.NewSecret("spec"),
			},
			wantKeyID: "AKIDSPEC",
		},
		{
			name: "assumeRole without keys takes the chain as its source",
			creds: connection.Credentials{
				Type: connection.CredentialsAssumeRole, RoleARN: testRoleARN, STSEndpoint: testSTSEndpoint,
			},
			stsResponse:  assumeRoleXML,
			wantKeyID:    roleKeyID,
			wantSTSSigns: keyChain,
		},
		{
			name: nameWebIdentity,
			env:  map[string]string{envProfile: missingProfile},
			creds: connection.Credentials{
				Type: connection.CredentialsWebIdentity, RoleARN: testRoleARN, TokenFile: tokenFile,
				STSEndpoint: testSTSEndpoint,
			},
			stsResponse: webIdentityXML,
			wantKeyID:   "ASIAWEB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hermeticAWSEnvironment(t)

			caFile := filepath.Join(t.TempDir(), "ca.pem")
			require.NoError(t, os.WriteFile(caFile, newTestCA(t, "env").pem, 0o600))

			base := map[string]string{
				"AWS_REGION": "ap-south-1", "AWS_DEFAULT_REGION": "ap-south-1",
				"AWS_ENDPOINT_URL": "http://hostile.test", "AWS_ENDPOINT_URL_S3": "http://hostile-s3.test",
				"AWS_ENDPOINT_URL_STS": "http://hostile-sts.test", envDefaultsMode: "standard",
				"AWS_CA_BUNDLE": caFile, "AWS_ACCESS_KEY_ID": keyChain, "AWS_SECRET_ACCESS_KEY": "chain-secret",
			}
			for key, value := range base {
				t.Setenv(key, value)
			}

			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			var s3Req, stsReq *http.Request

			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body := ""

				if req.URL.Host == hostSTS {
					stsReq = req
					body = tt.stsResponse
				} else {
					s3Req = req
				}

				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{headerType: {"text/xml"}},
					Body: io.NopCloser(strings.NewReader(body)), Request: req,
				}, nil
			})

			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set("a", other(func(spec *connection.S3Spec) {
				spec.Region = "eu-central-1"
				spec.Credentials = tt.creds
			})))

			client, err := fac.S3(t.Context(), "a")
			require.NoError(t, err)
			require.NoError(t, headObject(t, client))

			require.NotNil(t, s3Req)
			assert.Equal(t, "s3.test", s3Req.URL.Host)
			assert.Contains(t, s3Req.Header.Get("Authorization"), "Credential="+tt.wantKeyID+"/")
			assert.Contains(t, s3Req.Header.Get("Authorization"), "/eu-central-1/s3/")

			if tt.wantSTSSigns != "" {
				require.NotNil(t, stsReq)
				assert.Contains(t, stsReq.Header.Get("Authorization"), "Credential="+tt.wantSTSSigns+"/")
			}

			cfg, err := fac.AWSConfig(t.Context(), "a")
			require.NoError(t, err)
			assert.Equal(t, "eu-central-1", cfg.Region)
			assert.Equal(t, "http://s3.test", aws.ToString(cfg.BaseEndpoint))
		})
	}
}

// TestFactory_ChainNeedsAValidProcessEnvironment documents a limit: the SDK validates every AWS_* variable
// when it loads the credentials chain, and no load option skips that. A valid unrelated setting is harmless; an
// invalid one fails the chain connections (and only them) with an error that names the connection and says why.
// It is serial: see [hermeticAWSEnvironment].
func TestFactory_ChainNeedsAValidProcessEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{name: "valid defaults mode", key: envDefaultsMode, value: "standard"},
		{name: "invalid defaults mode", key: envDefaultsMode, value: valBogus, wantErr: true},
		{name: "invalid request checksum", key: "AWS_REQUEST_CHECKSUM_CALCULATION", value: valBogus, wantErr: true},
		{name: "invalid max attempts", key: "AWS_MAX_ATTEMPTS", value: "not-a-number", wantErr: true},
		{name: "missing profile", key: envProfile, value: missingProfile, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hermeticAWSEnvironment(t)

			t.Setenv("AWS_ACCESS_KEY_ID", keyChain)
			t.Setenv("AWS_SECRET_ACCESS_KEY", "chain-secret")
			t.Setenv(tt.key, tt.value)

			transport := newScripted(step{status: http.StatusOK})
			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set("broken", other(func(spec *connection.S3Spec) {
				spec.Credentials = connection.Credentials{}
			})))

			client, err := fac.S3(t.Context(), "broken")
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), `connection "broken"`)
				assert.Contains(t, err.Error(), "process AWS environment or shared config is invalid")
				assert.Contains(t, err.Error(), tt.value)
				assert.Equal(t, 0, transport.count())
				assert.Empty(t, fac.Builds())

				return
			}

			require.NoError(t, err)
			require.NoError(t, headObject(t, client))
			require.Equal(t, 1, transport.count())
			assert.Contains(t, transport.requests[0].Header.Get("Authorization"), "Credential=AKIDCHAIN/")
		})
	}
}

// TestFactory_DataPlaneIgnoresTheEnvironmentCABundle uses real TLS through the transport the factory builds,
// against a server signed by CA A, with a hostile AWS_CA_BUNDLE in the process environment. The environment
// bundle is never trusted by the data plane, only the CABundleFile of the spec is (the STS calls of assumeRole are
// covered by TestFactory_AssumeRoleHops). For a chain connection the SDK reads AWS_CA_BUNDLE too, for
// its own credential lookups, and refuses a bundle without certificates. It is serial: see
// [hermeticAWSEnvironment].
func TestFactory_DataPlaneIgnoresTheEnvironmentCABundle(t *testing.T) {
	staticCreds := connection.Credentials{
		Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret("AKIDSPEC"),
		SecretAccessKey: connection.NewSecret("spec"),
	}
	tests := []struct {
		name         string
		creds        connection.Credentials
		envBundle    string // "a", "b" or bundleGarbage: the content of AWS_CA_BUNDLE
		wantErr      string // on the request, empty: it succeeds
		wantBuildErr string // on the build of the client
		specBundle   bool   // the spec trusts CA A
	}{
		{
			name:      "static, environment bundle A is not trusted",
			creds:     staticCreds,
			envBundle: "a",
			wantErr:   unknownAuthority,
		},
		{
			name:       "static, spec bundle A, environment garbage",
			creds:      staticCreds,
			envBundle:  bundleGarbage,
			specBundle: true,
		},
		{name: "static, spec bundle A, environment bundle B", creds: staticCreds, envBundle: "b", specBundle: true},
		{
			name: "chain, environment bundle A is not trusted", creds: connection.Credentials{}, envBundle: "a",
			wantErr: unknownAuthority,
		},
		{
			name:       "chain, spec bundle A, environment bundle B",
			creds:      connection.Credentials{},
			envBundle:  "b",
			specBundle: true,
		},
		{
			name: "chain, the SDK refuses a garbage environment bundle", creds: connection.Credentials{},
			envBundle: bundleGarbage, specBundle: true, wantBuildErr: "credentials chain cannot be loaded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hermeticAWSEnvironment(t)

			caA, caB := newTestCA(t, "CA A"), newTestCA(t, "CA B")
			srv := caA.startServer(t, okHandler())

			dir := t.TempDir()
			bundles := map[string][]byte{"a": caA.pem, "b": caB.pem, bundleGarbage: []byte("not a certificate")}
			envFile, specFile := filepath.Join(dir, "env.pem"), filepath.Join(dir, "spec.pem")
			require.NoError(t, os.WriteFile(envFile, bundles[tt.envBundle], 0o600))
			require.NoError(t, os.WriteFile(specFile, caA.pem, 0o600))

			t.Setenv("AWS_CA_BUNDLE", envFile)
			t.Setenv("AWS_ACCESS_KEY_ID", keyChain)
			t.Setenv("AWS_SECRET_ACCESS_KEY", "chain-secret")

			fac := connection.New()
			require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) {
				spec.Endpoint = srv.URL
				spec.Credentials = tt.creds
				spec.Retry.MaxAttempts = 1

				if tt.specBundle {
					spec.CABundleFile = specFile
				}
			})))

			client, err := fac.S3(t.Context(), connC)
			if tt.wantBuildErr != "" {
				require.ErrorContains(t, err, tt.wantBuildErr)

				return
			}

			require.NoError(t, err)

			err = headObject(t, client)
			if tt.wantErr == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestFactory_AssumeRoleHops separates the two hops of an assumeRole connection, each against its own TLS
// server that counts the requests it completed and the TLS handshakes it refused: the STS call, then the S3
// request. Each case says which hop failed, so a failure of the S3 hop cannot pass for a failure of the STS one.
// It is serial: see [hermeticAWSEnvironment].
func TestFactory_AssumeRoleHops(t *testing.T) {
	tests := []struct {
		name           string
		bundle         string // the CAs of the spec bundle: hopSTS, hopS3, "both" or "none"
		envBundle      string // the CA of the hostile AWS_CA_BUNDLE: hopSTS or garbage
		wantFailingHop string // hopSTS, hopS3, or "" when both succeed
		wantSTSRequest int32
		wantS3Requests int32
		wantSTSRefused bool // the STS server refused at least one handshake
		wantS3Refused  bool
	}{
		{
			name: "no bundle: the environment CA of STS is not trusted", bundle: "none", envBundle: hopSTS,
			wantFailingHop: hopSTS, wantSTSRequest: 0, wantS3Requests: 0, wantSTSRefused: true,
		},
		{
			name: "bundle of STS only: STS answers, S3 is refused", bundle: hopSTS, envBundle: bundleGarbage,
			wantFailingHop: hopS3, wantSTSRequest: 1, wantS3Requests: 0, wantS3Refused: true,
		},
		{
			name: "bundle of S3 only: STS is refused first", bundle: hopS3, envBundle: bundleGarbage,
			wantFailingHop: hopSTS, wantSTSRequest: 0, wantS3Requests: 0, wantSTSRefused: true,
		},
		{
			name: "bundle of both: both hops succeed", bundle: "both", envBundle: bundleGarbage,
			wantFailingHop: "", wantSTSRequest: 1, wantS3Requests: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hermeticAWSEnvironment(t)

			caSTS, caS3 := newTestCA(t, "CA STS"), newTestCA(t, "CA S3")

			sts := caSTS.startCounting(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(headerType, "text/xml")
				_, _ = io.WriteString(w, assumeRoleXML)
			}))
			s3srv := caS3.startCounting(t, okHandler())

			bundles := map[string][]byte{
				hopSTS: caSTS.pem, hopS3: caS3.pem, "both": slices.Concat(caSTS.pem, caS3.pem),
			}
			dir := t.TempDir()
			specFile, envFile := filepath.Join(dir, "spec.pem"), filepath.Join(dir, "env.pem")
			require.NoError(t, os.WriteFile(specFile, bundles[tt.bundle], 0o600))

			envContent := []byte("not a certificate")
			if tt.envBundle == hopSTS {
				envContent = caSTS.pem
			}

			require.NoError(t, os.WriteFile(envFile, envContent, 0o600))
			t.Setenv("AWS_CA_BUNDLE", envFile)

			fac := connection.New()
			require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) {
				spec.Endpoint = s3srv.URL
				spec.Retry.MaxAttempts = 1
				spec.Credentials = connection.Credentials{
					Type: connection.CredentialsAssumeRole, RoleARN: testRoleARN, STSEndpoint: sts.URL,
					AccessKeyID: connection.NewSecret("AKIDSRC"), SecretAccessKey: connection.NewSecret("src"),
				}

				if tt.bundle != "none" {
					spec.CABundleFile = specFile
				}
			})))

			client, err := fac.S3(t.Context(), connC)
			require.NoError(t, err)

			err = headObject(t, client)
			if tt.wantFailingHop == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, unknownAuthority)
			}

			// the STS call comes first: if it completed no request, it is the hop that failed
			failingHop := ""

			switch {
			case err == nil:
			case sts.requests.Load() == 0:
				failingHop = hopSTS
			default:
				failingHop = hopS3
			}

			assert.Equal(t, tt.wantFailingHop, failingHop)
			assert.Equal(t, tt.wantSTSRequest, sts.requests.Load(), "requests completed by STS")
			assert.Equal(t, tt.wantS3Requests, s3srv.requests.Load(), "requests completed by S3")

			// the server logs a refused handshake after the client gave up: wait for the log, not for a delay
			for name, check := range map[string]struct {
				srv  *countingServer
				want bool
			}{hopSTS: {sts, tt.wantSTSRefused}, hopS3: {s3srv, tt.wantS3Refused}} {
				if check.want {
					require.Eventually(t, func() bool { return check.srv.refused.Load() > 0 },
						10*time.Second, 5*time.Millisecond, "%s refused no handshake", name)
				} else {
					assert.Equal(t, int32(0), check.srv.refused.Load(), "%s refused a handshake", name)
				}
			}
		})
	}
}
