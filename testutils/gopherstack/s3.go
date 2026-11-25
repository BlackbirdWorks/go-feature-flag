package gopherstack

import (
	"bytes"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
)

// S3Client returns an S3 client on the emulator.
func (s *Server) S3Client(tb testing.TB) *s3.Client {
	tb.Helper()

	return s3.NewFromConfig(s.MustAwsConfig(tb))
}

// CreateBucket creates a bucket named UniqueName(t, prefix) and returns its name.
func (s *Server) CreateBucket(tb testing.TB, prefix string) string {
	tb.Helper()
	name := UniqueName(tb, prefix)
	_, err := s.S3Client(tb).CreateBucket(tb.Context(), &s3.CreateBucketInput{Bucket: aws.String(name)})
	require.NoError(tb, err)

	return name
}

// PutObject uploads content at bucket/key.
func (s *Server) PutObject(tb testing.TB, bucket, key string, content []byte) {
	tb.Helper()
	_, err := s.S3Client(tb).PutObject(tb.Context(), &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(content),
	})
	require.NoError(tb, err)
}

// GetObject downloads bucket/key.
func (s *Server) GetObject(tb testing.TB, bucket, key string) []byte {
	tb.Helper()
	out, err := s.S3Client(tb).GetObject(tb.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(tb, err)

	defer func() { _ = out.Body.Close() }()

	b, err := io.ReadAll(out.Body)
	require.NoError(tb, err)

	return b
}

// ListKeys returns all the keys of the bucket under prefix.
func (s *Server) ListKeys(tb testing.TB, bucket, prefix string) []string {
	tb.Helper()

	var keys []string

	p := s3.NewListObjectsV2Paginator(s.S3Client(tb), &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket), Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(tb.Context())
		require.NoError(tb, err)

		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}

	return keys
}
