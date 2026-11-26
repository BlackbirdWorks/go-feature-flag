package connection_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

func assumeBase(spec *connection.S3Spec) {
	spec.Credentials = connection.Credentials{
		Type:            connection.CredentialsAssumeRole,
		RoleARN:         "arn:base",
		SessionName:     "s",
		ExternalID:      "e",
		STSEndpoint:     "http://sts.base",
		AccessKeyID:     connection.NewSecret("AKIDBASE"),
		SecretAccessKey: connection.NewSecret("base-secret"),
	}
}

func webBase(spec *connection.S3Spec) {
	spec.Credentials = connection.Credentials{
		Type: connection.CredentialsWebIdentity, RoleARN: "arn:base", SessionName: "s",
		TokenFile: "/tok-base", STSEndpoint: "http://sts.base",
	}
}

func TestIdentity_EachFieldChangesIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base func(*connection.S3Spec)
		mut  func(*connection.S3Spec)
		name string
	}{
		{name: "endpoint", mut: func(spec *connection.S3Spec) { spec.Endpoint = "http://other.test" }},
		{name: fieldRegion, mut: func(spec *connection.S3Spec) { spec.Region = testRegionEU }},
		{name: "bucket", mut: func(spec *connection.S3Spec) { spec.Bucket = bucketOther }},
		{name: "path style", mut: func(spec *connection.S3Spec) { spec.PathStyle = false }},
		{name: "connect timeout", mut: func(spec *connection.S3Spec) { spec.ConnectTimeout = time.Second }},
		{name: "request timeout", mut: func(spec *connection.S3Spec) { spec.RequestTimeout = time.Second }},
		{name: "retry attempts", mut: func(spec *connection.S3Spec) { spec.Retry.MaxAttempts = 9 }},
		{name: "retry backoff", mut: func(spec *connection.S3Spec) { spec.Retry.MaxBackoff = time.Hour }},
		{
			name: "request checksum",
			mut:  func(spec *connection.S3Spec) { spec.RequestChecksumCalculation = connection.ChecksumWhenRequired },
		},
		{
			name: "response checksum",
			mut:  func(spec *connection.S3Spec) { spec.ResponseChecksumValidation = connection.ChecksumWhenRequired },
		},
		{
			name: "static to chain",
			mut:  func(spec *connection.S3Spec) { spec.Credentials = connection.Credentials{} },
		},
		{
			name: "static access key id",
			mut:  func(spec *connection.S3Spec) { spec.Credentials.AccessKeyID = connection.NewSecret("AKIDOTHER") },
		},
		{
			name: "static secret access key",
			mut: func(spec *connection.S3Spec) {
				spec.Credentials.SecretAccessKey = connection.NewSecret("another-secret")
			},
		},
		{
			name: "static session token",
			mut:  func(spec *connection.S3Spec) { spec.Credentials.SessionToken = connection.NewSecret("tok") },
		},
		{
			name: "assume role arn",
			base: assumeBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.RoleARN = "arn:x" },
		},
		{
			name: "assume session name",
			base: assumeBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.SessionName = "x" },
		},
		{
			name: "assume external id",
			base: assumeBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.ExternalID = "x" },
		},
		{
			name: "assume sts endpoint", base: assumeBase,
			mut: func(spec *connection.S3Spec) { spec.Credentials.STSEndpoint = testSTSEndpoint },
		},
		{
			name: "assume source key",
			base: assumeBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.AccessKeyID = connection.NewSecret("AKIDX") },
		},
		{
			name: "assume source secret",
			base: assumeBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.SecretAccessKey = connection.NewSecret("x") },
		},
		{
			name: "assume source token",
			base: assumeBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.SessionToken = connection.NewSecret("x") },
		},
		{
			name: "web role arn",
			base: webBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.RoleARN = "arn:x" },
		},
		{
			name: "web session name",
			base: webBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.SessionName = "x" },
		},
		{
			name: "web token file",
			base: webBase,
			mut:  func(spec *connection.S3Spec) { spec.Credentials.TokenFile = "/x" },
		},
		{
			name: "web sts endpoint", base: webBase,
			mut: func(spec *connection.S3Spec) { spec.Credentials.STSEndpoint = testSTSEndpoint },
		},
		{
			name: "static to assumeRole",
			mut:  func(spec *connection.S3Spec) { spec.Credentials.Type = connection.CredentialsAssumeRole },
		},
	}

	seen := map[string]string{}

	for _, tt := range tests {
		baseSpec := testS3()
		if tt.base != nil {
			tt.base(baseSpec)
		}

		base := baseSpec.Identity()
		changed := other(func(spec *connection.S3Spec) {
			if tt.base != nil {
				tt.base(spec)
			}

			tt.mut(spec)
		}).Identity()

		assert.NotEqual(t, base, changed, tt.name)
		assert.NotContains(t, seen, changed, "%s collides with %s", tt.name, seen[changed])

		seen[changed] = tt.name
	}
}

func TestIdentity_Stability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a    connection.Spec
		b    connection.Spec
		name string
		want bool
	}{
		{name: "same spec twice", a: testS3(), b: testS3(), want: true},
		{
			name: "empty credentials type is chain",
			a:    other(func(spec *connection.S3Spec) { spec.Credentials = connection.Credentials{} }),
			b: other(func(spec *connection.S3Spec) {
				spec.Credentials = connection.Credentials{Type: connection.CredentialsChain}
			}),
			want: true,
		},
		{
			name: "chain ignores leftover keys and role fields",
			a:    other(func(spec *connection.S3Spec) { spec.Credentials = connection.Credentials{} }),
			b: other(func(spec *connection.S3Spec) {
				spec.Credentials = connection.Credentials{
					Type:            connection.CredentialsChain,
					AccessKeyID:     connection.NewSecret("AKIDLEFT"),
					SecretAccessKey: connection.NewSecret("left"),
					SessionToken:    connection.NewSecret("t"),
					RoleARN:         "arn:left",
					SessionName:     "s",
					ExternalID:      "e",
					TokenFile:       "/t",
					STSEndpoint:     testSTSEndpoint,
				}
			}),
			want: true,
		},
		{
			name: "static ignores role fields",
			a:    testS3(),
			b: other(func(spec *connection.S3Spec) {
				spec.Credentials.RoleARN = "arn:left"
				spec.Credentials.TokenFile = "/t"
				spec.Credentials.ExternalID = "e"
			}),
			want: true,
		},
		{
			name: "webIdentity ignores keys and external id",
			a:    other(webBase),
			b: other(func(spec *connection.S3Spec) {
				webBase(spec)
				spec.Credentials.AccessKeyID = connection.NewSecret("AKIDLEFT")
				spec.Credentials.ExternalID = "e"
			}),
			want: true,
		},
		{
			name: "assumeRole ignores the token file",
			a:    other(assumeBase),
			b: other(func(spec *connection.S3Spec) {
				assumeBase(spec)
				spec.Credentials.TokenFile = "/left"
			}),
			want: true,
		},
		{
			name: "fields do not slide into each other",
			a:    other(func(spec *connection.S3Spec) { spec.Endpoint = "http://a.test"; spec.Region = "bc" }),
			b:    other(func(spec *connection.S3Spec) { spec.Endpoint = "http://a.testb"; spec.Region = "c" }),
			want: false,
		},
		{
			name: "kind is part of the identity",
			a:    &connection.AWSSpec{Type: connection.KindSQS, Common: testS3().Common},
			b:    &connection.AWSSpec{Type: connection.KindKinesis, Common: testS3().Common},
			want: false,
		},
		{
			name: "s3 and aws differ even with equal common settings",
			a:    testS3(),
			b:    &connection.AWSSpec{Type: connection.KindSQS, Common: testS3().Common},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.a.Identity() == tt.b.Identity())
		})
	}
}

func TestIdentity_NeverContainsASecret(t *testing.T) {
	t.Parallel()

	s := other(func(spec *connection.S3Spec) {
		spec.Credentials.AccessKeyID = connection.NewSecret("AKIDCLEARTEXT")
		spec.Credentials.SecretAccessKey = connection.NewSecret("SECRETCLEARTEXT")
		spec.Credentials.SessionToken = connection.NewSecret("TOKENCLEARTEXT")
	})
	id := s.Identity()

	require.NotEmpty(t, id)

	for _, secret := range []string{"AKIDCLEARTEXT", "SECRETCLEARTEXT", "TOKENCLEARTEXT"} {
		assert.NotContains(t, id, secret)
	}

	assert.Regexp(t, `^[0-9a-f]{64}$`, id)
}
