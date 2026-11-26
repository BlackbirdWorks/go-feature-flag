package connection_test

import (
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// writeCABundle writes the certificate of a TLS test server as a PEM bundle and returns its path.
func writeCABundle(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ca.pem")
	block := &pem.Block{Type: pemCertificate, Bytes: srv.Certificate().Raw}
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(block), 0o600))

	return path
}

// hostileEnvironment makes the process environment as unfriendly as possible to a connection. The
// caller must not be parallel: the SDK only reads the real process environment.
func hostileEnvironment(t *testing.T, caBundle string) {
	t.Helper()

	t.Setenv("AWS_REGION", "ap-south-1")
	t.Setenv("AWS_DEFAULT_REGION", "ap-south-1")
	t.Setenv("AWS_ENDPOINT_URL", "http://hostile.test")
	t.Setenv("AWS_ENDPOINT_URL_S3", "http://hostile-s3.test")
	t.Setenv("AWS_ENDPOINT_URL_STS", "http://hostile-sts.test")
	t.Setenv("AWS_DEFAULTS_MODE", "standard")
	t.Setenv("AWS_CA_BUNDLE", caBundle)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", os.DevNull)
	t.Setenv("AWS_CONFIG_FILE", os.DevNull)
}

// TestFactory_ChainAndRolesIgnoreTheEnvironment checks the connections that use the SDK chain, the roles and
// the web identity against a hostile environment: only the chain credentials come from it.
//
// It is not parallel because the SDK only reads the real process environment (t.Setenv).
func TestFactory_ChainAndRolesIgnoreTheEnvironment(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	hostileEnvironment(t, writeCABundle(t, srv))
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDCHAIN")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "chain-secret")

	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("jwt"), 0o600))

	tests := []struct {
		name         string
		creds        connection.Credentials
		stsResponse  string
		wantKeyID    string // key id that signs the S3 request
		wantSTSSigns string // key id that signs the STS request, when there is one
	}{
		{
			name:      "chain uses the environment credentials only",
			creds:     connection.Credentials{},
			wantKeyID: "AKIDCHAIN",
		},
		{
			name: "assumeRole without keys takes the chain as its source",
			creds: connection.Credentials{
				Type: connection.CredentialsAssumeRole, RoleARN: testRoleARN, STSEndpoint: testSTSEndpoint,
			},
			stsResponse:  assumeRoleXML,
			wantKeyID:    roleKeyID,
			wantSTSSigns: "AKIDCHAIN",
		},
		{
			name: nameWebIdentity,
			creds: connection.Credentials{
				Type: connection.CredentialsWebIdentity, RoleARN: testRoleARN, TokenFile: tokenFile,
				STSEndpoint: testSTSEndpoint,
			},
			stsResponse: webIdentityXML,
			wantKeyID:   "ASIAWEB",
		},
	}

	for _, tt := range tests {
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
		})), tt.name)

		client, err := fac.S3(t.Context(), "a")
		require.NoError(t, err, tt.name)
		require.NoError(t, headObject(t, client), tt.name)

		require.NotNil(t, s3Req, tt.name)
		assert.Equal(t, "s3.test", s3Req.URL.Host, tt.name)
		assert.Contains(t, s3Req.Header.Get("Authorization"), "Credential="+tt.wantKeyID+"/", tt.name)
		assert.Contains(t, s3Req.Header.Get("Authorization"), "/eu-central-1/s3/", tt.name)

		if tt.wantSTSSigns != "" {
			require.NotNil(t, stsReq, tt.name)
			assert.Contains(t, stsReq.Header.Get("Authorization"), "Credential="+tt.wantSTSSigns+"/", tt.name)
		}
	}
}

func TestFactory_CABundleFile(t *testing.T) {
	t.Parallel()

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)

	bundle := writeCABundle(t, srv)

	tests := []struct {
		name    string
		bundle  string
		wantObs []observation
		wantErr bool
	}{
		{
			name:    "private CA is rejected without the bundle",
			wantErr: true,
			wantObs: []observation{{connC, opHead, resError}},
		},
		{name: "private CA is trusted with the bundle", bundle: bundle, wantObs: []observation{{connC, opHead, resOK}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &recorder{}
			fac := connection.New(connection.WithObserver(rec))
			require.NoError(t, fac.Set(connC, other(func(spec *connection.S3Spec) {
				spec.Endpoint = srv.URL
				spec.CABundleFile = tt.bundle
				spec.Retry.MaxAttempts = 1
			})))

			client, err := fac.S3(t.Context(), connC)
			require.NoError(t, err)

			err = headObject(t, client)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, tt.wantObs, rec.all())
		})
	}
}

func TestFactory_CABundleFileValidation(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)

	good := pem.EncodeToMemory(&pem.Block{Type: pemCertificate, Bytes: srv.Certificate().Raw})

	tests := []struct {
		files   map[string][]byte
		name    string
		path    string
		wantErr bool
	}{
		{name: "valid bundle", files: map[string][]byte{testCAPath: good}, path: testCAPath},
		{name: "missing file", files: map[string][]byte{}, path: testCAPath, wantErr: true},
		{name: "empty file", files: map[string][]byte{testCAPath: {}}, path: testCAPath, wantErr: true},
		{
			name:    "no certificate in the file",
			files:   map[string][]byte{testCAPath: []byte("not pem")},
			path:    testCAPath,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New(connection.WithReadFile(func(path string) ([]byte, error) {
				data, ok := tt.files[path]
				if !ok {
					return nil, os.ErrNotExist
				}

				return data, nil
			}))

			spec := other(func(spec *connection.S3Spec) { spec.CABundleFile = tt.path })
			err := fac.Set(connC, spec)

			if !tt.wantErr {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, connection.ErrInvalidSpec)

			var specErr *connection.SpecError

			require.ErrorAs(t, err, &specErr)
			assert.Equal(t, "caBundleFile", specErr.Field)
			assert.Equal(t, connC, specErr.Connection)
			assert.Empty(t, fac.Names())
		})
	}
}

// TestFactory_ChainNeedsAValidProcessEnvironment documents a limit: the SDK validates every AWS_* variable
// when it loads the credentials chain, and no load option skips that. A valid unrelated setting is harmless; an
// invalid one fails the chain connections (and only them) with an error that names the connection and says why.
//
// It is not parallel because the SDK only reads the real process environment (t.Setenv).
func TestFactory_ChainNeedsAValidProcessEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{name: "valid defaults mode", key: "AWS_DEFAULTS_MODE", value: "standard"},
		{name: "invalid defaults mode", key: "AWS_DEFAULTS_MODE", value: valBogus, wantErr: true},
		{name: "invalid request checksum", key: "AWS_REQUEST_CHECKSUM_CALCULATION", value: valBogus, wantErr: true},
		{name: "invalid max attempts", key: "AWS_MAX_ATTEMPTS", value: "not-a-number", wantErr: true},
		{name: "missing profile", key: "AWS_PROFILE", value: "profile-that-does-not-exist", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AWS_ACCESS_KEY_ID", "AKIDCHAIN")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "chain-secret")
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
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
