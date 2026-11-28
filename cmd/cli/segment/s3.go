package segment

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// S3Client is the part of the S3 client that a source needs: *s3.Client implements it.
type S3Client interface {
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectsV2(
		ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options),
	) (*s3.ListObjectsV2Output, error)
}

// S3Objects is a bucket of an S3 client as [segmentadmin.ObjectAPI].
type S3Objects struct {
	client S3Client
	bucket string
}

var _ segmentadmin.ObjectAPI = (*S3Objects)(nil)

// NewS3Objects returns the objects of a bucket, read with client.
func NewS3Objects(client S3Client, bucket string) *S3Objects {
	return &S3Objects{client: client, bucket: bucket}
}

// Head implements [segmentadmin.ObjectAPI].
func (o *S3Objects) Head(ctx context.Context, key string) (segmentadmin.ObjectInfo, error) {
	out, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &o.bucket, Key: &key})
	if err != nil {
		return segmentadmin.ObjectInfo{}, classify(err)
	}

	return segmentadmin.ObjectInfo{Key: key, ETag: aws.ToString(out.ETag), Size: aws.ToInt64(out.ContentLength)}, nil
}

// List implements [segmentadmin.ObjectAPI].
func (o *S3Objects) List(ctx context.Context, prefix, token string) (segmentadmin.ObjectPage, error) {
	in := &s3.ListObjectsV2Input{Bucket: &o.bucket, Prefix: &prefix}
	if token != "" {
		in.ContinuationToken = &token
	}

	out, err := o.client.ListObjectsV2(ctx, in)
	if err != nil {
		return segmentadmin.ObjectPage{}, classify(err)
	}

	page := segmentadmin.ObjectPage{}

	for i := range out.Contents {
		item := &out.Contents[i]
		page.Objects = append(page.Objects, segmentadmin.ObjectInfo{
			Key: aws.ToString(item.Key), ETag: aws.ToString(item.ETag), Size: aws.ToInt64(item.Size),
		})
	}

	if aws.ToBool(out.IsTruncated) {
		page.Next = aws.ToString(out.NextContinuationToken)
	}

	return page, nil
}

// Get implements [segmentadmin.ObjectAPI]: one GET, conditional on the ETag, whose body is read as it arrives.
func (o *S3Objects) Get(ctx context.Context, key, etag string) (segmentadmin.Object, error) {
	in := &s3.GetObjectInput{Bucket: &o.bucket, Key: &key}
	if etag != "" {
		in.IfMatch = &etag
	}

	out, err := o.client.GetObject(ctx, in)
	if err != nil {
		return segmentadmin.Object{}, classify(err)
	}

	return segmentadmin.Object{
		Body: out.Body, Size: aws.ToInt64(out.ContentLength),
		Gzip: strings.EqualFold(aws.ToString(out.ContentEncoding), "gzip"),
	}, nil
}

// classify maps the answers that the source acts on; any other error is the SDK's.
func classify(err error) error {
	status := 0
	if respErr, found := errors.AsType[*awshttp.ResponseError](err); found {
		status = respErr.HTTPStatusCode()
	}

	apiErr, isAPI := errors.AsType[smithy.APIError](err)
	_, noKey := errors.AsType[*types.NoSuchKey](err)
	_, notFound := errors.AsType[*types.NotFound](err)

	switch {
	case status == http.StatusPreconditionFailed || isAPI && apiErr.ErrorCode() == "PreconditionFailed":
		return fmt.Errorf("%w: %w", segmentadmin.ErrObjectChanged, err)
	case noKey || notFound || status == http.StatusNotFound && (!isAPI || apiErr.ErrorCode() != "NoSuchBucket"):
		return fmt.Errorf("%w: %w", segmentadmin.ErrObjectMissing, err)
	}

	return err
}
