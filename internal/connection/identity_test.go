package connection_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

func TestIdentity_EachFieldChangesIt(t *testing.T) {
	t.Parallel()

	base := testS3().Identity()

	tests := []struct {
		mut  func(*connection.S3Spec)
		name string
	}{
		{name: "endpoint", mut: func(spec *connection.S3Spec) { spec.Endpoint = "http://other.test" }},
		{name: "region", mut: func(spec *connection.S3Spec) { spec.Region = testRegionEU }},
		{name: "bucket", mut: func(spec *connection.S3Spec) { spec.Bucket = bucketOther }},
		{name: "path style", mut: func(spec *connection.S3Spec) { spec.PathStyle = false }},
		{
			name: "credentials type",
			mut:  func(spec *connection.S3Spec) { spec.Credentials.Type = connection.CredentialsAssumeRole },
		},
		{name: "access key id", mut: func(spec *connection.S3Spec) { spec.Credentials.AccessKeyID = "AKIDOTHER" }},
		{
			name: "secret access key",
			mut:  func(spec *connection.S3Spec) { spec.Credentials.SecretAccessKey = "another-secret" },
		},
		{name: "session token", mut: func(spec *connection.S3Spec) { spec.Credentials.SessionToken = "tok" }},
		{name: "role arn", mut: func(spec *connection.S3Spec) { spec.Credentials.RoleARN = testRoleARN }},
		{name: "session name", mut: func(spec *connection.S3Spec) { spec.Credentials.SessionName = "sn" }},
		{name: "external id", mut: func(spec *connection.S3Spec) { spec.Credentials.ExternalID = "ext" }},
		{name: "token file", mut: func(spec *connection.S3Spec) { spec.Credentials.TokenFile = "/tok" }},
		{name: "sts endpoint", mut: func(spec *connection.S3Spec) { spec.Credentials.STSEndpoint = testSTSEndpoint }},
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
	}

	seen := map[string]string{base: "base"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			id := other(tt.mut).Identity()
			assert.NotEqual(t, base, id)
		})

		// distinct changes must also be distinct from each other
		id := other(tt.mut).Identity()
		assert.NotContains(t, seen, id, "%s collides with %s", tt.name, seen[id])
		seen[id] = tt.name
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
		spec.Credentials.AccessKeyID = "AKIDCLEARTEXT"
		spec.Credentials.SecretAccessKey = "SECRETCLEARTEXT"
		spec.Credentials.SessionToken = "TOKENCLEARTEXT"
	})
	id := s.Identity()

	require.NotEmpty(t, id)

	for _, secret := range []string{"AKIDCLEARTEXT", "SECRETCLEARTEXT", "TOKENCLEARTEXT"} {
		assert.NotContains(t, id, secret)
	}

	assert.Regexp(t, `^[0-9a-f]{64}$`, id)
}
