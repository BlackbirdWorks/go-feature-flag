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
func (s *Server) S3Client(t testing.TB) *s3.Client {
	t.Helper()

	return s3.NewFromConfig(s.MustAwsConfig(t))
}

// CreateBucket creates a bucket named UniqueName(t, prefix) and returns its name.
func (s *Server) CreateBucket(t testing.TB, prefix string) string {
	t.Helper()
	name := UniqueName(t, prefix)
	_, err := s.S3Client(t).CreateBucket(t.Context(), &s3.CreateBucketInput{Bucket: aws.String(name)})
	require.NoError(t, err)

	return name
}

// PutObject uploads content at bucket/key.
func (s *Server) PutObject(t testing.TB, bucket, key string, content []byte) {
	t.Helper()
	_, err := s.S3Client(t).PutObject(t.Context(), &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(content),
	})
	require.NoError(t, err)
}

// GetObject downloads bucket/key.
func (s *Server) GetObject(t testing.TB, bucket, key string) []byte {
	t.Helper()
	out, err := s.S3Client(t).GetObject(t.Context(), &s3.GetObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(key),
	})
	require.NoError(t, err)
	defer func() { _ = out.Body.Close() }()
	b, err := io.ReadAll(out.Body)
	require.NoError(t, err)

	return b
}

// ListKeys returns all the keys of the bucket under prefix.
func (s *Server) ListKeys(t testing.TB, bucket, prefix string) []string {
	t.Helper()
	var keys []string
	p := s3.NewListObjectsV2Paginator(s.S3Client(t), &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket), Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(t.Context())
		require.NoError(t, err)
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}

	return keys
}
