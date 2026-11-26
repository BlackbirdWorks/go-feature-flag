package connection_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

func other(mut func(*connection.S3Spec)) *connection.S3Spec {
	spec := *testS3()
	mut(&spec)

	return &spec
}

func TestFactory_OneClientPerConnection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		specs      map[string]connection.Spec
		wantBuilds map[string]int
		name       string
		ask        []string
		wantSame   bool
	}{
		{
			name:       "many callers of one connection share one client",
			specs:      map[string]connection.Spec{"a": testS3()},
			ask:        []string{"a", "a", "a", "a"},
			wantSame:   true,
			wantBuilds: map[string]int{"a": 1},
		},
		{
			name:       "two connections build two clients",
			specs:      map[string]connection.Spec{"a": testS3(), "b": testS3()},
			ask:        []string{"a", "b"},
			wantSame:   false,
			wantBuilds: map[string]int{"a": 1, "b": 1},
		},
		{
			name:       "a connection nobody asked for builds nothing",
			specs:      map[string]connection.Spec{"a": testS3(), "b": testS3()},
			ask:        []string{"a"},
			wantSame:   true,
			wantBuilds: map[string]int{"a": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New()
			require.NoError(t, fac.Replace(tt.specs))

			clients := make([]any, 0, len(tt.ask))

			for _, name := range tt.ask {
				c, err := fac.S3(t.Context(), name)
				require.NoError(t, err)

				clients = append(clients, c)
			}

			same := true

			for _, c := range clients {
				if c != clients[0] {
					same = false
				}
			}

			assert.Equal(t, tt.wantSame, same)
			assert.Equal(t, tt.wantBuilds, fac.Builds())
		})
	}
}

func TestFactory_AWSConfigAndS3ShareTheBuild(t *testing.T) {
	t.Parallel()

	fac := connection.New()
	require.NoError(t, fac.Set("a", testS3()))

	cfg, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)
	_, err = fac.S3(t.Context(), "a")
	require.NoError(t, err)
	cfg2, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)

	assert.Equal(t, map[string]int{"a": 1}, fac.Builds())
	assert.Equal(t, "us-east-1", cfg.Region)
	assert.Equal(t, cfg.Region, cfg2.Region)
}

func TestFactory_ConcurrentS3BuildsOnce(t *testing.T) {
	t.Parallel()

	const callers = 64

	fac := connection.New()
	require.NoError(t, fac.Set("a", testS3()))

	var wg sync.WaitGroup

	results := make([]any, callers)
	errs := make([]error, callers)
	start := make(chan struct{})

	for i := range callers {
		wg.Go(func() {
			<-start

			results[i], errs[i] = fac.S3(t.Context(), "a")
		})
	}

	close(start)
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Same(t, results[0], results[i])
	}

	assert.Equal(t, map[string]int{"a": 1}, fac.Builds())
}

func TestFactory_Reload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		before     map[string]connection.Spec
		after      map[string]connection.Spec
		wantReused map[string]bool
		wantBuilds map[string]int
		name       string
		wantGone   []string
	}{
		{
			name:       "unchanged is reused",
			before:     map[string]connection.Spec{"a": testS3()},
			after:      map[string]connection.Spec{"a": testS3()},
			wantReused: map[string]bool{"a": true},
			wantBuilds: map[string]int{"a": 1},
		},
		{
			name:   "changed endpoint is rebuilt, unchanged neighbour is reused",
			before: map[string]connection.Spec{"a": testS3(), "b": testS3()},
			after: map[string]connection.Spec{
				"a": other(func(spec *connection.S3Spec) { spec.Endpoint = "http://other.test" }),
				"b": testS3(),
			},
			wantReused: map[string]bool{"a": false, "b": true},
			wantBuilds: map[string]int{"a": 2, "b": 1},
		},
		{
			name:   "changed secret is rebuilt",
			before: map[string]connection.Spec{"a": testS3()},
			after: map[string]connection.Spec{
				"a": other(
					func(spec *connection.S3Spec) { spec.Credentials.SecretAccessKey = connection.NewSecret("rotated") },
				),
			},
			wantReused: map[string]bool{"a": false},
			wantBuilds: map[string]int{"a": 2},
		},
		{
			name:       "removed is dropped",
			before:     map[string]connection.Spec{"a": testS3(), "b": testS3()},
			after:      map[string]connection.Spec{"a": testS3()},
			wantReused: map[string]bool{"a": true},
			wantGone:   []string{"b"},
			wantBuilds: map[string]int{"a": 1, "b": 1},
		},
		{
			name:       "added is available",
			before:     map[string]connection.Spec{"a": testS3()},
			after:      map[string]connection.Spec{"a": testS3(), "c": testS3()},
			wantReused: map[string]bool{"a": true, "c": false},
			wantBuilds: map[string]int{"a": 1, "c": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New()
			require.NoError(t, fac.Replace(tt.before))

			old := map[string]any{}

			for name := range tt.before {
				c, err := fac.S3(t.Context(), name)
				require.NoError(t, err)

				old[name] = c
			}

			require.NoError(t, fac.Replace(tt.after))

			for name, wantReused := range tt.wantReused {
				c, err := fac.S3(t.Context(), name)
				require.NoError(t, err)

				prev, had := old[name]
				assert.Equal(t, wantReused, had && prev == any(c), name)
			}

			for _, name := range tt.wantGone {
				_, err := fac.S3(t.Context(), name)
				require.ErrorIs(t, err, connection.ErrUnknownConnection)
			}

			assert.Equal(t, tt.wantBuilds, fac.Builds())
		})
	}
}

func TestFactory_SetKeepsClientWhenIdentityIsUnchanged(t *testing.T) {
	t.Parallel()

	fac := connection.New()
	require.NoError(t, fac.Set("a", testS3()))

	first, err := fac.S3(t.Context(), "a")
	require.NoError(t, err)

	require.NoError(t, fac.Set("a", testS3()))

	second, err := fac.S3(t.Context(), "a")
	require.NoError(t, err)

	assert.Same(t, first, second)

	fac.Remove("a")
	fac.Remove("never-there")
	assert.Empty(t, fac.Names())
}

func TestFactory_InvalidReplaceChangesNothing(t *testing.T) {
	t.Parallel()

	fac := connection.New()
	require.NoError(t, fac.Replace(map[string]connection.Spec{"a": testS3()}))

	err := fac.Replace(map[string]connection.Spec{
		"b": testS3(),
		"c": other(func(spec *connection.S3Spec) { spec.RequestChecksumCalculation = "sometimes" }),
	})
	require.ErrorIs(t, err, connection.ErrInvalidSpec)

	var se *connection.SpecError

	require.ErrorAs(t, err, &se)
	assert.Equal(t, "c", se.Connection)
	assert.Equal(t, "requestChecksumCalculation", se.Field)
	assert.Equal(t, []string{"a"}, fac.Names())
}

func TestFactory_Errors(t *testing.T) {
	t.Parallel()

	fac := connection.New()
	require.NoError(t, fac.Set("queue", &connection.AWSSpec{Type: connection.KindSQS, Common: testS3().Common}))

	t.Run("unknown connection from S3", func(t *testing.T) {
		t.Parallel()

		_, err := fac.S3(t.Context(), "nope")
		require.ErrorIs(t, err, connection.ErrUnknownConnection)

		var ue *connection.UnknownConnectionError

		require.ErrorAs(t, err, &ue)
		assert.Equal(t, "nope", ue.Name)
		assert.Equal(t, `unknown connection "nope"`, err.Error())
	})

	t.Run("unknown connection from AWSConfig", func(t *testing.T) {
		t.Parallel()

		_, err := fac.AWSConfig(t.Context(), "nope")
		require.ErrorIs(t, err, connection.ErrUnknownConnection)
	})

	t.Run("S3 client of a non S3 connection", func(t *testing.T) {
		t.Parallel()

		_, err := fac.S3(t.Context(), "queue")
		require.ErrorIs(t, err, connection.ErrWrongKind)

		var ke *connection.KindError

		require.ErrorAs(t, err, &ke)
		assert.Equal(t, "sqs", ke.Got)
		assert.Equal(t, "s3", ke.Want)
	})

	t.Run("nil spec", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, connection.New().Set("a", nil), connection.ErrInvalidSpec)
	})
}

func TestSpec_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		spec      connection.Spec
		wantField string // empty: valid
	}{
		{"valid s3", testS3(), ""},
		{"valid aws", &connection.AWSSpec{Type: connection.KindKinesis, Common: testS3().Common}, ""},
		{"s3 region is required", other(func(spec *connection.S3Spec) { spec.Region = "" }), fieldRegion},
		{"aws region is required", &connection.AWSSpec{Type: connection.KindSQS}, fieldRegion},
		{"assumeRole with source key pair", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{
				Type:            connection.CredentialsAssumeRole,
				RoleARN:         testRoleARN,
				AccessKeyID:     connection.NewSecret("a"),
				SecretAccessKey: connection.NewSecret("b"),
			}
		}), ""},
		{"assumeRole on the chain", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{Type: connection.CredentialsAssumeRole, RoleARN: testRoleARN}
		}), ""},
		{"assumeRole with only an access key", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{
				Type: connection.CredentialsAssumeRole, RoleARN: testRoleARN, AccessKeyID: connection.NewSecret("a"),
			}
		}), fieldCreds},
		{"assumeRole with only a secret key", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{
				Type:            connection.CredentialsAssumeRole,
				RoleARN:         testRoleARN,
				SecretAccessKey: connection.NewSecret("b"),
			}
		}), fieldCreds},
		{"assumeRole with only a session token", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{
				Type: connection.CredentialsAssumeRole, RoleARN: testRoleARN, SessionToken: connection.NewSecret("t"),
			}
		}), fieldCreds},
		{"kind is required", &connection.AWSSpec{}, "type"},
		{"bad endpoint", other(func(spec *connection.S3Spec) { spec.Endpoint = "localhost:4566" }), "endpoint"},
		{"negative timeout", other(func(spec *connection.S3Spec) { spec.RequestTimeout = -time.Second }), "timeouts"},
		{"negative retry", other(func(spec *connection.S3Spec) { spec.Retry.MaxAttempts = -1 }), "retry"},
		{
			"bad checksum request",
			other(func(spec *connection.S3Spec) { spec.RequestChecksumCalculation = "x" }),
			"requestChecksumCalculation",
		},
		{
			"bad checksum response",
			other(func(spec *connection.S3Spec) { spec.ResponseChecksumValidation = "x" }),
			"responseChecksumValidation",
		},
		{
			"unknown credentials type",
			other(func(spec *connection.S3Spec) { spec.Credentials.Type = "magic" }),
			"credentials.type",
		},
		{"static without keys", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{Type: connection.CredentialsStatic}
		}), fieldCreds},
		{"assumeRole without arn", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{Type: connection.CredentialsAssumeRole}
		}), fieldCreds},
		{"webIdentity without token file", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{Type: connection.CredentialsWebIdentity, RoleARN: testRoleARN}
		}), fieldCreds},
		{"bad sts endpoint", other(func(spec *connection.S3Spec) {
			spec.Credentials = connection.Credentials{
				Type:        connection.CredentialsAssumeRole,
				RoleARN:     testRoleARN,
				STSEndpoint: "sts",
			}
		}), "credentials.stsEndpoint"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.spec.Validate()
			if tt.wantField == "" {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, connection.ErrInvalidSpec)

			var se *connection.SpecError

			require.ErrorAs(t, err, &se)
			assert.Equal(t, tt.wantField, se.Field)
		})
	}
}

func TestSpec_Kind(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "s3", testS3().Kind())

	redshift := &connection.AWSSpec{Type: connection.KindRedshift}
	assert.Equal(t, "redshift", redshift.Kind())
}

func TestFactory_ChecksumOptionResolution(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		request      connection.ChecksumMode
		response     connection.ChecksumMode
		wantRequest  aws.RequestChecksumCalculation
		wantResponse aws.ResponseChecksumValidation
		wantErr      bool
	}{
		{
			"unset keeps the SDK default",
			"",
			"",
			aws.RequestChecksumCalculationWhenSupported,
			aws.ResponseChecksumValidationWhenSupported,
			false,
		},
		{
			"request whenRequired",
			connection.ChecksumWhenRequired,
			"",
			aws.RequestChecksumCalculationWhenRequired,
			aws.ResponseChecksumValidationWhenSupported,
			false,
		},
		{
			"response whenRequired",
			"",
			connection.ChecksumWhenRequired,
			aws.RequestChecksumCalculationWhenSupported,
			aws.ResponseChecksumValidationWhenRequired,
			false,
		},
		{
			"both whenRequired",
			connection.ChecksumWhenRequired,
			connection.ChecksumWhenRequired,
			aws.RequestChecksumCalculationWhenRequired,
			aws.ResponseChecksumValidationWhenRequired,
			false,
		},
		{
			"both whenSupported",
			connection.ChecksumWhenSupported,
			connection.ChecksumWhenSupported,
			aws.RequestChecksumCalculationWhenSupported,
			aws.ResponseChecksumValidationWhenSupported,
			false,
		},
		{"invalid request", "always", "", 0, 0, true},
		{"invalid response", "", "never", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New()

			err := fac.Set("a", other(func(spec *connection.S3Spec) {
				spec.RequestChecksumCalculation = tt.request
				spec.ResponseChecksumValidation = tt.response
			}))
			if tt.wantErr {
				require.ErrorIs(t, err, connection.ErrInvalidSpec)

				return
			}

			require.NoError(t, err)

			cfg, err := fac.AWSConfig(t.Context(), "a")
			require.NoError(t, err)
			assert.Equal(t, tt.wantRequest, cfg.RequestChecksumCalculation)
			assert.Equal(t, tt.wantResponse, cfg.ResponseChecksumValidation)
		})
	}
}

func TestFactory_EndpointAndRegionPerConnection(t *testing.T) {
	t.Parallel()

	fac := connection.New()
	require.NoError(t, fac.Replace(map[string]connection.Spec{
		"eu": other(func(spec *connection.S3Spec) { spec.Region = testRegionEU; spec.Endpoint = "http://eu.test" }),
		"us": other(func(spec *connection.S3Spec) { spec.Region = "us-west-2"; spec.Endpoint = "http://us.test" }),
	}))

	for name, want := range map[string][2]string{
		"eu": {testRegionEU, "http://eu.test"},
		"us": {"us-west-2", "http://us.test"},
	} {
		cfg, err := fac.AWSConfig(t.Context(), name)
		require.NoError(t, err)
		assert.Equal(t, want[0], cfg.Region, name)
		assert.Equal(t, want[1], aws.ToString(cfg.BaseEndpoint), name)
	}
}

const (
	assumeRoleXML = `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><AssumeRoleResult>` +
		`<Credentials><AccessKeyId>ASIAROLE</AccessKeyId><SecretAccessKey>role-secret</SecretAccessKey>` +
		`<SessionToken>role-token</SessionToken><Expiration>2099-01-01T00:00:00Z</Expiration></Credentials>` +
		`</AssumeRoleResult></AssumeRoleResponse>`
	webIdentityXML = `<AssumeRoleWithWebIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">` +
		`<AssumeRoleWithWebIdentityResult><Credentials><AccessKeyId>ASIAWEB</AccessKeyId>` +
		`<SecretAccessKey>web-secret</SecretAccessKey><SessionToken>web-token</SessionToken>` +
		`<Expiration>2099-01-01T00:00:00Z</Expiration></Credentials></AssumeRoleWithWebIdentityResult>` +
		`</AssumeRoleWithWebIdentityResponse>`
)

func TestFactory_Credentials(t *testing.T) {
	t.Parallel()

	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("jwt-token"), 0o600))

	tests := []struct {
		name         string
		creds        connection.Credentials
		stsResponse  string
		wantAccessID string
		wantSTSForm  map[string]string // parameters the STS request must carry
		wantSTSHost  string
		wantSTSCalls int
	}{
		{
			name: nameStatic,
			creds: connection.Credentials{
				Type:            connection.CredentialsStatic,
				AccessKeyID:     connection.NewSecret("AKIDSTATIC"),
				SecretAccessKey: connection.NewSecret("s"),
				SessionToken:    connection.NewSecret("t"),
			},
			wantAccessID: "AKIDSTATIC",
		},
		{
			name: "assumeRole with static source",
			creds: connection.Credentials{
				Type:            connection.CredentialsAssumeRole,
				AccessKeyID:     connection.NewSecret("AKIDSRC"),
				SecretAccessKey: connection.NewSecret("s"),
				RoleARN:         testRoleFull,
				SessionName:     testSession,
				ExternalID:      "ext-1",
				STSEndpoint:     testSTSEndpoint,
			},
			stsResponse:  assumeRoleXML,
			wantAccessID: roleKeyID,
			wantSTSForm: map[string]string{
				formAction:        "AssumeRole",
				formRoleArn:       testRoleFull,
				"RoleSessionName": testSession,
				"ExternalId":      "ext-1",
			},
			wantSTSHost:  hostSTS,
			wantSTSCalls: 1,
		},
		{
			name: "assumeRole without an STS endpoint does not use the connection endpoint",
			creds: connection.Credentials{
				Type:            connection.CredentialsAssumeRole,
				AccessKeyID:     connection.NewSecret("AKIDSRC"),
				SecretAccessKey: connection.NewSecret("s"),
				RoleARN:         testRoleFull,
				SessionName:     testSession,
			},
			stsResponse:  assumeRoleXML,
			wantAccessID: roleKeyID,
			wantSTSForm:  map[string]string{formAction: "AssumeRole", formRoleArn: testRoleFull},
			wantSTSHost:  "sts.us-east-1.amazonaws.com",
			wantSTSCalls: 1,
		},
		{
			name: nameWebIdentity,
			creds: connection.Credentials{
				Type: connection.CredentialsWebIdentity, RoleARN: "arn:aws:iam::1:role/web", SessionName: "websess",
				TokenFile: tokenFile, STSEndpoint: testSTSEndpoint,
			},
			stsResponse:  webIdentityXML,
			wantAccessID: "ASIAWEB",
			wantSTSForm: map[string]string{
				formAction: "AssumeRoleWithWebIdentity", formRoleArn: "arn:aws:iam::1:role/web",
				"RoleSessionName": "websess", "WebIdentityToken": "jwt-token",
			},
			wantSTSHost:  hostSTS,
			wantSTSCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu    sync.Mutex
				forms []url.Values
				hosts []string
			)

			transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					return nil, err
				}

				form, err := url.ParseQuery(string(body))
				if err != nil {
					return nil, err
				}

				mu.Lock()

				forms = append(forms, form)
				hosts = append(hosts, req.URL.Host)
				mu.Unlock()

				return &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{headerType: {"text/xml"}},
					Body: io.NopCloser(stringsReader(tt.stsResponse)), Request: req,
				}, nil
			})

			fac := connection.New(connection.WithRoundTripper(transport))
			require.NoError(t, fac.Set("a", other(func(spec *connection.S3Spec) { spec.Credentials = tt.creds })))

			cfg, err := fac.AWSConfig(t.Context(), "a")
			require.NoError(t, err)

			got, err := cfg.Credentials.Retrieve(t.Context())
			require.NoError(t, err)
			assert.Equal(t, tt.wantAccessID, got.AccessKeyID)

			mu.Lock()
			defer mu.Unlock()

			require.Len(t, forms, tt.wantSTSCalls)

			for i := range forms {
				for k, v := range tt.wantSTSForm {
					assert.Equal(t, v, forms[i].Get(k), k)
				}

				assert.Equal(t, tt.wantSTSHost, hosts[i])
			}
		})
	}
}

func TestFactory_CredentialsProviderNeverPrints(t *testing.T) {
	t.Parallel()

	const sentinel = "SENTINEL-KEY-MATERIAL"

	tests := []struct {
		name  string
		creds connection.Credentials
	}{
		{name: "chain", creds: connection.Credentials{}},
		{name: nameStatic, creds: connection.Credentials{
			Type: connection.CredentialsStatic, AccessKeyID: connection.NewSecret(sentinel),
			SecretAccessKey: connection.NewSecret(sentinel), SessionToken: connection.NewSecret(sentinel),
		}},
		{name: "assumeRole", creds: connection.Credentials{
			Type: connection.CredentialsAssumeRole, RoleARN: testRoleARN, AccessKeyID: connection.NewSecret(sentinel),
			SecretAccessKey: connection.NewSecret(sentinel),
		}},
		{name: nameWebIdentity, creds: connection.Credentials{
			Type: connection.CredentialsWebIdentity, RoleARN: testRoleARN, TokenFile: "/token",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fac := connection.New()
			require.NoError(t, fac.Set("a", other(func(spec *connection.S3Spec) { spec.Credentials = tt.creds })))

			cfg, err := fac.AWSConfig(t.Context(), "a")
			require.NoError(t, err)
			require.NotNil(t, cfg.Credentials)

			for _, format := range []string{"%v", verbPlusV, verbGoV, "%s", "%d", "%x"} {
				assert.Equal(t, connection.RedactedValue, fmt.Sprintf(format, cfg.Credentials), format)
				assert.NotContains(t, fmt.Sprintf(format, cfg), sentinel, format)
			}
		})
	}
}

func TestFactory_HTTPClientOverride(t *testing.T) {
	t.Parallel()

	transport := newScripted()
	httpClient := &http.Client{Transport: transport}
	fac := connection.New(connection.WithHTTPClient(httpClient))
	require.NoError(t, fac.Set("a", testS3()))

	cfg, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)
	assert.Same(t, httpClient, cfg.HTTPClient)
}

func TestFactory_DefaultHTTPClientIsPerConnection(t *testing.T) {
	t.Parallel()

	fac := connection.New()
	require.NoError(t, fac.Replace(map[string]connection.Spec{
		"a": other(
			func(spec *connection.S3Spec) { spec.ConnectTimeout = time.Second; spec.RequestTimeout = time.Minute },
		),
		"b": testS3(),
	}))

	a, err := fac.AWSConfig(t.Context(), "a")
	require.NoError(t, err)
	b, err := fac.AWSConfig(t.Context(), "b")
	require.NoError(t, err)

	assert.NotSame(t, a.HTTPClient, b.HTTPClient)
}
