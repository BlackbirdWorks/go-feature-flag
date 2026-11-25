package testutils

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
)

var (
	errCannotCreateContextFrom = errors.New("cannot create context from nil parent")
	errInvalidBucket           = errors.New("invalid bucket")
	errWriterAtIsRequired      = errors.New("WriterAt is required")
	errNoFile                  = errors.New("no file")
)

type S3ManagerV2Mock struct {
	S3ManagerMockFileSystem map[string]string
	TestDataLocation        string
}

func (s *S3ManagerV2Mock) UploadObject(
	ctx context.Context,
	uploadInput *transfermanager.UploadObjectInput,
	_ ...func(*transfermanager.Options),
) (*transfermanager.UploadObjectOutput, error) {
	if ctx == nil {
		return nil, errCannotCreateContextFrom
	}

	if uploadInput.Bucket == nil || *uploadInput.Bucket == "" {
		return nil, errInvalidBucket
	}

	if s.S3ManagerMockFileSystem == nil {
		s.S3ManagerMockFileSystem = make(map[string]string)
	}

	buf := new(strings.Builder)

	_, err := io.Copy(buf, uploadInput.Body)
	if err != nil {
		fmt.Println(err) //nolint:forbidigo // mock server diagnostics
	}

	s.S3ManagerMockFileSystem[*uploadInput.Key] = buf.String()

	return &transfermanager.UploadObjectOutput{
		Location: uploadInput.Key,
	}, nil
}

func (s *S3ManagerV2Mock) DownloadObject(
	ctx context.Context,
	input *transfermanager.DownloadObjectInput,
	_ ...func(*transfermanager.Options),
) (*transfermanager.DownloadObjectOutput, error) {
	if ctx == nil {
		return nil, errCannotCreateContextFrom
	}

	if input.WriterAt == nil {
		return nil, errWriterAtIsRequired
	}

	if input.Key != nil && *input.Key == "valid" {
		res, _ := os.ReadFile(s.TestDataLocation + "/flag-config.yaml")
		_, _ = input.WriterAt.WriteAt(res, 0)

		return &transfermanager.DownloadObjectOutput{}, nil
	}

	if input.Key != nil && *input.Key == "no-file" {
		return nil, errNoFile
	}

	return &transfermanager.DownloadObjectOutput{}, nil
}
