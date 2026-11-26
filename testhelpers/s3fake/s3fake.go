// Package s3fake is an in-memory S3 bucket for tests: GetObject, HeadObject and PutObject with the conditional
// write semantics of S3 (If-None-Match: * and If-Match answer 412 PreconditionFailed, If-Match on a missing key
// answers NoSuchKey), a verified ChecksumSHA256, a log of every call in order, and a hook to inject failures or
// play another writer. It implements the API of controlplane/s3publish.
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
	violations []string
	calls      []string
	seq        int
	mu         sync.Mutex
}

// New returns an empty bucket. The manifest at manifestKey is checked after every write: each object it lists must
// exist (see Fake.AssertConsistent).
func New(manifestKey string) *Fake {
	return &Fake{objects: map[string]*fakeObject{}, manifestKey: manifestKey}
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

	err := f.record("GET " + key)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

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

	return &s3.HeadObjectOutput{
		ETag: aws.String(
			obj.etag,
		),
		ContentLength:  aws.Int64(int64(len(obj.body))),
		ChecksumSHA256: aws.String(obj.sha256),
	}, nil
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

// AssertConsistent fails the test if a manifest ever listed an object that was not in the bucket.
func (f *Fake) AssertConsistent(t *testing.T) {
	t.Helper()

	assert.Empty(t, f.Violations(), "a manifest listed an object that was not in the bucket")
}

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
	f.seq++
	sum := md5.Sum(body) //nolint:gosec // see import
	raw := sha256.Sum256(body)
	obj := &fakeObject{
		etag: fmt.Sprintf("\"%s-%d\"", hex.EncodeToString(sum[:4]), f.seq), body: bytes.Clone(body),
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
		if _, ok := f.objects[manifest.Objects[i].Key]; !ok {
			f.violations = append(f.violations,
				fmt.Sprintf("generation %d lists missing %s", manifest.Generation, manifest.Objects[i].Key))
		}
	}
}
