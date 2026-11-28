package segmentadmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// DefaultMaxObjects is the most objects of a prefix that an import reads when no bound is given.
const DefaultMaxObjects = 1000

var (
	// ErrObjectURL is returned for a source that is not s3://bucket/key or s3://bucket/prefix/.
	ErrObjectURL = errors.New(
		"the source must be s3://<bucket>/<key>, or s3://<bucket>/<prefix>/ for every object under it",
	)
	// ErrObjectMissing is returned when the object of a source does not exist.
	ErrObjectMissing = errors.New("the source object does not exist")
	// ErrObjectChanged is returned by [ObjectAPI.Get] when the ETag no longer matches: the object was replaced.
	ErrObjectChanged = errors.New("the source object changed while it was read")
	// ErrTooManyObjects is returned when a prefix holds more objects than the bound: nothing is read.
	ErrTooManyObjects = errors.New("the prefix holds more objects than --max-objects")
	// ErrSourceChanged is returned when the objects of a source are not the ones that were listed when the run began:
	// nothing is swapped, and the next run starts again.
	ErrSourceChanged = errors.New("the source changed while it was read: nothing was swapped")
)

// ObjectRef names the object, or the prefix of the objects, of a source.
type ObjectRef struct {
	// Bucket is the bucket of the URL.
	Bucket string
	// Key is the key of the object, or the prefix of the objects.
	Key string
	// Prefix tells that Key is a prefix (the URL ends with a slash): every object under it is read.
	Prefix bool
}

// ParseObjectURL reads s3://bucket/key and s3://bucket/prefix/.
func ParseObjectURL(raw string) (ObjectRef, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "s3" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ObjectRef{}, ErrObjectURL
	}

	key := strings.TrimPrefix(parsed.Path, "/")
	prefix := key == "" || strings.HasSuffix(key, "/")

	return ObjectRef{Bucket: parsed.Host, Key: key, Prefix: prefix}, nil
}

// ObjectInfo is what a listing or a HEAD says about an object.
type ObjectInfo struct {
	// Key is the key of the object.
	Key string
	// ETag is the validator of the content.
	ETag string
	// Size is the size of the stored bytes.
	Size int64
}

// ObjectPage is one page of a listing.
type ObjectPage struct {
	// Next is the token of the next page, empty on the last.
	Next string
	// Objects are the objects of the page, in key order.
	Objects []ObjectInfo
}

// Object is the answer of a GET.
type Object struct {
	// Body is the content; the caller closes it.
	Body io.ReadCloser
	// Size is the length that the server announced, zero or less when it did not.
	Size int64
	// Gzip tells that Content-Encoding is gzip.
	Gzip bool
}

// ObjectAPI is the bucket that holds the objects of a source, as the code needs it. The adapter of a client
// library implements it; a test injects a fake. The bucket is part of the implementation.
type ObjectAPI interface {
	// Head returns the object, or [ErrObjectMissing].
	Head(ctx context.Context, key string) (ObjectInfo, error)
	// List returns a page of the objects under prefix, in key order. An empty token starts the listing.
	List(ctx context.Context, prefix, token string) (ObjectPage, error)
	// Get streams an object of one ETag (If-Match): when the object has another one, the answer is
	// [ErrObjectChanged]. The body is read as it arrives, never held whole.
	Get(ctx context.Context, key, etag string) (Object, error)
}

// ObjectSource is the objects of one source URL.
type ObjectSource struct {
	api        ObjectAPI
	ref        ObjectRef
	maxObjects int
}

// NewObjectSource returns the source of ref in the bucket of api. A prefix may hold maxObjects objects at most (zero
// means [DefaultMaxObjects]).
func NewObjectSource(api ObjectAPI, ref ObjectRef, maxObjects int) *ObjectSource {
	if maxObjects <= 0 {
		maxObjects = DefaultMaxObjects
	}

	return &ObjectSource{api: api, ref: ref, maxObjects: maxObjects}
}

// fingerprintOf hashes the keys, ETags and sizes of the objects, which are in key order.
func fingerprintOf(objects []ObjectInfo) string {
	sum := sha256.New()

	for _, info := range objects {
		_, _ = io.WriteString(sum, info.Key+"\x00"+info.ETag+"\x00"+strconv.FormatInt(info.Size, 10)+"\n")
	}

	return "sha256:" + hex.EncodeToString(sum.Sum(nil))
}

// Fingerprint is the identity of the content of the source: one object (HEAD), or the keys and ETags of the
// listing of a prefix, hashed into one value. It reads no content.
func (s *ObjectSource) Fingerprint(ctx context.Context) (string, error) {
	objects, err := s.list(ctx)
	if err != nil {
		return "", err
	}

	return fingerprintOf(objects), nil
}

// Open lists the source and starts a stream of its objects, one after the other, with a line break between two of
// them. With a non-empty want, a listing that has another fingerprint is [ErrSourceChanged]. The stream is a [Source];
// it checks at its end that the listing still has the fingerprint, and it fails when it does not.
func (s *ObjectSource) Open(ctx context.Context, want string, opts ReaderOptions) (*ObjectStream, error) {
	objects, err := s.list(ctx)
	if err != nil {
		return nil, err
	}

	fingerprint := fingerprintOf(objects)
	if want != "" && want != fingerprint {
		return nil, ErrSourceChanged
	}

	body := &objectBody{
		objects: objects,
		open:    func(info ObjectInfo) (Object, error) { return s.api.Get(ctx, info.Key, info.ETag) },
	}

	reader, err := NewReader(body, opts)
	if err != nil {
		return nil, err
	}

	verify := func() error {
		again, listErr := s.Fingerprint(ctx)
		if listErr != nil {
			return listErr
		}

		if again != fingerprint {
			return ErrSourceChanged
		}

		return nil
	}

	return &ObjectStream{reader: reader, body: body, verify: verify, fingerprint: fingerprint}, nil
}

// list returns the objects of the source in key order.
func (s *ObjectSource) list(ctx context.Context) ([]ObjectInfo, error) {
	if !s.ref.Prefix {
		info, err := s.api.Head(ctx, s.ref.Key)
		if err != nil {
			return nil, fmt.Errorf("head the source object: %w", err)
		}

		return []ObjectInfo{info}, nil
	}

	var (
		found []ObjectInfo
		token string
	)

	for {
		page, err := s.api.List(ctx, s.ref.Key, token)
		if err != nil {
			return nil, fmt.Errorf("list the source prefix: %w", err)
		}

		for _, info := range page.Objects {
			if strings.HasSuffix(info.Key, "/") {
				continue // a folder marker
			}

			if len(found) >= s.maxObjects {
				return nil, fmt.Errorf("%w (%d)", ErrTooManyObjects, s.maxObjects)
			}

			found = append(found, info)
		}

		if page.Next == "" {
			break
		}

		token = page.Next
	}

	slices.SortFunc(found, func(a, b ObjectInfo) int { return strings.Compare(a.Key, b.Key) })

	return found, nil
}
