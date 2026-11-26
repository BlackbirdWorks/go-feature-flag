// Package s3fake is an in-memory S3 bucket for tests: GetObject, HeadObject and PutObject with the conditional
// write semantics of S3 (If-None-Match: * and If-Match answer 412 PreconditionFailed, If-Match on a missing key
// answers NoSuchKey), a verified ChecksumSHA256, a log of every call in order, and a hook to inject failures or
// play another writer. It implements the API of controlplane/s3publish.
//
// Where S3 documents a behaviour the fake follows it: a single-part ETag is the quoted hex md5 of the body, and
// If-None-Match accepts only "*" (any other value answers 501 NotImplemented, so a test cannot rely on it).
package s3fake

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // the fake builds ETags like S3 does
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/controlplane"
)

type fakeObject struct {
	etag   string
	sha256 string
	body   []byte
}

// Fake is an in-memory bucket with S3 conditional-write semantics. It records every call in order as a string:
// "GET key", "HEAD key", "PUT key create", "PUT key if-match=<etag>" or "PUT key" (unconditional).
// It is safe for concurrent use.
type Fake struct {
	objects     map[string]*fakeObject
	manifestKey string
	// Before runs at the start of every call, outside the lock, with the call string. A non-nil error fails the
	// call. It may use Seed and Remove to play another publisher.
	Before func(call string) error
	// violations lists the moments when the manifest listed an object that did not exist.
	versions   map[string]*fakeObject
	violations []string
	calls      []string
	mu         sync.Mutex
	// OmitChecksums makes HeadObject report no ChecksumSHA256 even when ChecksumMode is enabled, as a store does
	// for an object uploaded without one. Set it before the fake is used. Without it HeadObject reports the
	// checksum only when the request sets ChecksumMode to ENABLED, as S3 does.
	OmitChecksums bool
}

// New returns an empty bucket. When PutObject successfully writes manifestKey, each object that the manifest lists
// must exist with its listed size and checksum, or a violation is recorded (see Fake.AssertConsistent). Seed,
// Remove and writes to other keys are not checked: they are the deliberate bypass for fixtures and fault injection.
func New(manifestKey string) *Fake {
	return &Fake{objects: map[string]*fakeObject{}, versions: map[string]*fakeObject{}, manifestKey: manifestKey}
}

// SeedVersion stores a version of an object under its version id. GetObject with that VersionId returns it;
// GetObject without a VersionId returns the object stored with Seed. An unknown version answers 404 NoSuchVersion.
func (f *Fake) SeedVersion(key, versionID string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()

	sum := md5.Sum(body) //nolint:gosec // see import
	raw := sha256.Sum256(body)
	f.versions[key+"\x00"+versionID] = &fakeObject{
		etag: `"` + hex.EncodeToString(sum[:]) + `"`, body: bytes.Clone(body),
		sha256: base64.StdEncoding.EncodeToString(raw[:]),
	}
}

// Seed stores an object as another writer would, without recording a call, and returns its ETag.
func (f *Fake) Seed(key string, body []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.storeLocked(key, body)
}

// Remove deletes an object without recording a call.
func (f *Fake) Remove(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.objects, key)
}

// Body returns a copy of the object and whether it exists.
func (f *Fake) Body(key string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	obj, ok := f.objects[key]
	if !ok {
		return nil, false
	}

	return bytes.Clone(obj.body), true
}

// ETag returns the ETag of the object, or "".
func (f *Fake) ETag(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if obj, ok := f.objects[key]; ok {
		return obj.etag
	}

	return ""
}

// Calls returns the recorded calls in order.
func (f *Fake) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

// ResetCalls forgets the recorded calls.
func (f *Fake) ResetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = nil
}

// Keys returns the sorted keys of the bucket.
func (f *Fake) Keys() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	keys := make([]string, 0, len(f.objects))
	for k := range f.objects {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// GetObject implements s3publish.API.
func (f *Fake) GetObject(
	_ context.Context,
	input *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	key := aws.ToString(input.Key)

	call := "GET " + key
	if input.VersionId != nil {
		call += " version=" + aws.ToString(input.VersionId)
	}

	err := f.record(call)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if input.VersionId != nil {
		version, found := f.versions[key+"\x00"+aws.ToString(input.VersionId)]
		if !found {
			return nil, &StatusError{Code: "NoSuchVersion", Status: http.StatusNotFound}
		}

		return &s3.GetObjectOutput{
			Body: io.NopCloser(bytes.NewReader(version.body)), ETag: aws.String(version.etag),
			ContentLength: aws.Int64(int64(len(version.body))),
		}, nil
	}

	obj, ok := f.objects[key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}

	return &s3.GetObjectOutput{
		Body: io.NopCloser(
			bytes.NewReader(obj.body),
		),
		ETag:          aws.String(obj.etag),
		ContentLength: aws.Int64(int64(len(obj.body))),
	}, nil
}

// HeadObject implements s3publish.API.
func (f *Fake) HeadObject(
	_ context.Context,
	input *s3.HeadObjectInput,
	_ ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	key := aws.ToString(input.Key)

	err := f.record("HEAD " + key)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	obj, ok := f.objects[key]
	if !ok {
		return nil, &types.NotFound{}
	}

	out := &s3.HeadObjectOutput{ETag: aws.String(obj.etag), ContentLength: aws.Int64(int64(len(obj.body)))}
	if !f.OmitChecksums && input.ChecksumMode == types.ChecksumModeEnabled {
		out.ChecksumSHA256 = aws.String(obj.sha256)
	}

	return out, nil
}

// PutObject implements s3publish.API.
func (f *Fake) PutObject(
	_ context.Context,
	input *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	key := aws.ToString(input.Key)

	call := "PUT " + key

	switch {
	case input.IfNoneMatch != nil:
		call += " create"
		if aws.ToString(input.IfNoneMatch) != "*" {
			call += "?"
		}
	case input.IfMatch != nil:
		call += " if-match=" + aws.ToString(input.IfMatch)
	}

	err := f.record(call)
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(input.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	err = f.checkPut(input, key, body)
	if err != nil {
		return nil, err
	}

	etag := f.storeLocked(key, body)
	if key == f.manifestKey {
		f.checkInvariantLocked(key)
	}

	return &s3.PutObjectOutput{ETag: aws.String(etag)}, nil
}

// Violations lists the moments when a manifest listed an object that was not in the bucket.
func (f *Fake) Violations() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.violations)
}

// AssertConsistent fails the test if a manifest written by PutObject ever listed an object that was not in the
// bucket or had another size or checksum. A manifest stored with Seed is not checked.
func (f *Fake) AssertConsistent(t *testing.T) {
	t.Helper()

	assert.Empty(t, f.Violations(), "a manifest listed an object that was not in the bucket")
}

// StatusError is an S3 error with a code and an HTTP status, as the SDK returns for a response error.
type StatusError struct {
	Code   string
	Status int
}

// Error implements error.
func (e *StatusError) Error() string { return fmt.Sprintf("%d %s", e.Status, e.Code) }

// ErrorCode implements smithy.APIError.
func (e *StatusError) ErrorCode() string { return e.Code }

// ErrorMessage implements smithy.APIError.
func (e *StatusError) ErrorMessage() string { return e.Code }

// ErrorFault implements smithy.APIError.
func (*StatusError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

// HTTPStatusCode returns the HTTP status of the response.
func (e *StatusError) HTTPStatusCode() int { return e.Status }

// APIError is an S3 error with a code, as the SDK returns it: use it in a Before hook.
func APIError(code string) error { return &smithy.GenericAPIError{Code: code, Message: code} }

func (f *Fake) record(call string) error {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	hook := f.Before
	f.mu.Unlock()

	if hook != nil {
		return hook(call)
	}

	return nil
}

func (f *Fake) storeLocked(key string, body []byte) string {
	sum := md5.Sum(body) //nolint:gosec // see import
	raw := sha256.Sum256(body)
	obj := &fakeObject{
		etag: `"` + hex.EncodeToString(sum[:]) + `"`, body: bytes.Clone(body),
		sha256: base64.StdEncoding.EncodeToString(raw[:]),
	}
	f.objects[key] = obj

	return obj.etag
}

func (f *Fake) checkPut(input *s3.PutObjectInput, key string, body []byte) error {
	existing, exists := f.objects[key]

	if input.ChecksumSHA256 != nil {
		raw := sha256.Sum256(body)
		if aws.ToString(input.ChecksumSHA256) != base64.StdEncoding.EncodeToString(raw[:]) {
			return &smithy.GenericAPIError{Code: "BadDigest", Message: "sha256 mismatch"}
		}
	}

	if input.IfNoneMatch != nil && aws.ToString(input.IfNoneMatch) != "*" {
		return &smithy.GenericAPIError{Code: "NotImplemented", Message: "If-None-Match supports only *"}
	}

	if input.IfNoneMatch != nil && exists {
		return &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "exists"}
	}

	if input.IfMatch != nil {
		if !exists {
			return &types.NoSuchKey{}
		}

		if existing.etag != aws.ToString(input.IfMatch) {
			return &smithy.GenericAPIError{Code: "PreconditionFailed", Message: "etag"}
		}
	}

	return nil
}

// checkInvariantLocked records a violation when the manifest at key lists an object that is not in the bucket.
func (f *Fake) checkInvariantLocked(key string) {
	manifest, _, err := controlplane.UnmarshalManifest(f.objects[key].body)
	if err != nil {
		f.violations = append(f.violations, "unreadable manifest: "+err.Error())

		return
	}

	for i := range manifest.Objects {
		listed := &manifest.Objects[i]

		stored, ok := f.objects[listed.Key]

		switch {
		case !ok:
			f.violations = append(f.violations,
				fmt.Sprintf("generation %d lists missing %s", manifest.Generation, listed.Key))
		case int64(len(stored.body)) != listed.Size:
			f.violations = append(f.violations,
				fmt.Sprintf("generation %d lists %s with size %d, stored %d",
					manifest.Generation, listed.Key, listed.Size, len(stored.body)))
		case controlplane.VerifyChecksum(stored.body, listed.Checksum) != nil:
			f.violations = append(f.violations,
				fmt.Sprintf("generation %d lists %s with another checksum", manifest.Generation, listed.Key))
		}
	}
}
