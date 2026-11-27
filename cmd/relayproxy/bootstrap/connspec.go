package bootstrap

import (
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/internal/connection"
)

// ConnectionSpec turns an AWS backed connection of the file into the spec the connection factory takes, with
// its secrets resolved. A connection of type file has no spec (it is not a client) and gives nil. The error
// names the setting, never a value.
//
//nolint:ireturn // the factory takes the interface
func ConnectionSpec(
	conn Connection,
	opts LoadOptions,
) (connection.Spec, error) {
	switch {
	case conn.S3 != nil:
		return S3Spec(conn.S3, opts)
	case conn.Kinesis != nil:
		return awsSpec(TypeKinesis, &conn.Kinesis.AWS, opts)
	case conn.SQS != nil:
		return awsSpec(TypeSQS, &conn.SQS.AWS, opts)
	case conn.Redshift != nil:
		return awsSpec(TypeRedshift, &conn.Redshift.AWS, opts)
	}

	return nil, nil //nolint:nilnil // a file connection has no client: no spec is the answer
}

// S3Spec turns the S3 connection of the bootstrap file into the spec of the factory, resolving the secrets.
func S3Spec(conn *S3Connection, opts LoadOptions) (*connection.S3Spec, error) {
	common, err := commonSpec(&conn.AWS, opts)
	if err != nil {
		return nil, err
	}

	return &connection.S3Spec{
		Common:                     common,
		Bucket:                     conn.Bucket,
		RequestChecksumCalculation: connection.ChecksumMode(conn.RequestChecksumCalculation),
		ResponseChecksumValidation: connection.ChecksumMode(conn.ResponseChecksumValidation),
		PathStyle:                  conn.PathStyle,
	}, nil
}

func awsSpec(kind string, aws *AWS, opts LoadOptions) (*connection.AWSSpec, error) {
	common, err := commonSpec(aws, opts)
	if err != nil {
		return nil, err
	}

	return &connection.AWSSpec{Common: common, Type: kind}, nil
}

func commonSpec(aws *AWS, opts LoadOptions) (connection.Common, error) {
	creds, err := specCredentials(aws.Credentials, opts)
	if err != nil {
		return connection.Common{}, err
	}

	com := connection.Common{
		Credentials:  creds,
		Endpoint:     aws.Endpoint,
		Region:       aws.Region,
		CABundleFile: aws.CABundleFile,
	}

	if aws.Timeouts != nil {
		com.ConnectTimeout = specDuration(aws.Timeouts.Connect)
		com.RequestTimeout = specDuration(aws.Timeouts.Request)
	}

	if aws.Retry != nil {
		com.Retry.MaxBackoff = specDuration(aws.Retry.MaxBackoff)

		if aws.Retry.MaxAttempts != nil {
			com.Retry.MaxAttempts = *aws.Retry.MaxAttempts
		}
	}

	return com, nil
}

func specDuration(d *Duration) time.Duration {
	if d == nil {
		return 0
	}

	return time.Duration(*d)
}

func specCredentials(cred *Credentials, opts LoadOptions) (connection.Credentials, error) {
	if cred == nil {
		return connection.Credentials{Type: connection.CredentialsChain}, nil
	}

	out := connection.Credentials{
		Type:        connection.CredentialsType(cred.Type),
		RoleARN:     cred.RoleARN,
		SessionName: cred.SessionName,
		TokenFile:   cred.TokenFile,
	}

	ext, err := resolveValue(cred.ExternalID, opts)
	if err != nil {
		return out, fmt.Errorf("credentials.externalId: %w", err)
	}

	out.ExternalID = ext

	for _, field := range []struct {
		dst  *connection.Secret
		name string
		src  Secret
	}{
		{&out.AccessKeyID, keyAccessKeyID, cred.AccessKeyID},
		{&out.SecretAccessKey, keySecretKey, cred.SecretAccessKey},
		{&out.SessionToken, "sessionToken", cred.SessionToken},
	} {
		value, resolveErr := resolveValue(field.src, opts)
		if resolveErr != nil {
			return out, fmt.Errorf("credentials.%s: %w", field.name, resolveErr)
		}

		*field.dst = connection.NewSecret(value)
	}

	return out, nil
}

func resolveValue(s Secret, opts LoadOptions) (string, error) {
	if !s.IsSet() {
		return "", nil
	}

	r, err := s.Resolve(opts)
	if err != nil {
		return "", err
	}

	return r.Reveal(), nil
}
