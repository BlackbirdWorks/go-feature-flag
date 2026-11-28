package admintest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// ErrReadFailed is the failure that an [Objects] body gives after the bytes that [Objects.BreakAfter] allows.
var ErrReadFailed = errors.New("the connection broke")

type object struct {
	data   []byte
	gzip   bool
	noETag bool
}

// Objects is an in-memory bucket that implements [segmentadmin.ObjectAPI]. It is safe for concurrent use. The ETag of
// an object is the hash of its bytes.
type Objects struct {
	objs      map[string]object
	breakAt   map[string]int
	onGet     func(key string)
	calls     []string
	mu        sync.Mutex
	pageSize  int
	headFails int
}

var _ segmentadmin.ObjectAPI = (*Objects)(nil)

// NewObjects returns an empty bucket whose listing pages hold pageSize objects (3 when zero).
func NewObjects(pageSize int) *Objects {
	if pageSize <= 0 {
		pageSize = 3
	}

	return &Objects{objs: map[string]object{}, breakAt: map[string]int{}, pageSize: pageSize}
}

// Put stores an object. With gzip, the object is served with Content-Encoding gzip (the bytes are the caller's).
func (o *Objects) Put(key string, data []byte, gzip bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.objs[key] = object{data: slices.Clone(data), gzip: gzip}
}

// WithoutETag makes the store answer an object with an empty ETag, as some S3 compatible stores do.
func (o *Objects) WithoutETag(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	obj := o.objs[key]
	obj.noETag = true
	o.objs[key] = obj
}

// Remove deletes an object.
func (o *Objects) Remove(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	delete(o.objs, key)
}

// BreakAfter makes the GET of key fail with [ErrReadFailed] after n bytes of its body.
func (o *Objects) BreakAfter(key string, n int) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.breakAt[key] = n
}

// OnGet sets a function that runs at the start of every GET, before the object is read (a test replaces an object
// there).
func (o *Objects) OnGet(fn func(key string)) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.onGet = fn
}

// FailHeads makes the next n HEAD and LIST calls fail with [ErrReadFailed].
func (o *Objects) FailHeads(n int) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.headFails = n
}

// Calls returns the calls so far ("HEAD key", "LIST prefix", "GET key").
func (o *Objects) Calls() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	return slices.Clone(o.calls)
}

func etagOf(obj object) string {
	if obj.noETag {
		return ""
	}

	sum := sha256.Sum256(obj.data)

	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// Head implements [segmentadmin.ObjectAPI].
func (o *Objects) Head(ctx context.Context, key string) (segmentadmin.ObjectInfo, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.calls = append(o.calls, "HEAD "+key)

	err := errors.Join(ctx.Err(), o.fail())
	if err != nil {
		return segmentadmin.ObjectInfo{}, err
	}

	obj, ok := o.objs[key]
	if !ok {
		return segmentadmin.ObjectInfo{}, segmentadmin.ErrObjectMissing
	}

	return segmentadmin.ObjectInfo{Key: key, ETag: etagOf(obj), Size: int64(len(obj.data))}, nil
}

// List implements [segmentadmin.ObjectAPI]. The token is the last key of the previous page.
func (o *Objects) List(ctx context.Context, prefix, token string) (segmentadmin.ObjectPage, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.calls = append(o.calls, "LIST "+prefix)

	err := errors.Join(ctx.Err(), o.fail())
	if err != nil {
		return segmentadmin.ObjectPage{}, err
	}

	var keys []string

	for key := range o.objs {
		if strings.HasPrefix(key, prefix) && key > token {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)

	page := segmentadmin.ObjectPage{}

	if len(keys) > o.pageSize {
		keys = keys[:o.pageSize]
		page.Next = keys[len(keys)-1]
	}

	for _, key := range keys {
		obj := o.objs[key]
		page.Objects = append(
			page.Objects,
			segmentadmin.ObjectInfo{Key: key, ETag: etagOf(obj), Size: int64(len(obj.data))},
		)
	}

	return page, nil
}

// Get implements [segmentadmin.ObjectAPI].
func (o *Objects) Get(ctx context.Context, key, etag string) (segmentadmin.Object, error) {
	o.mu.Lock()
	hook := o.onGet
	o.mu.Unlock()

	if hook != nil {
		hook(key)
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.calls = append(o.calls, "GET "+key)

	err := ctx.Err()
	if err != nil {
		return segmentadmin.Object{}, fmt.Errorf("get: %w", err)
	}

	obj, ok := o.objs[key]

	switch {
	case !ok:
		return segmentadmin.Object{}, segmentadmin.ErrObjectMissing
	case etag != "" && etag != etagOf(obj):
		return segmentadmin.Object{}, segmentadmin.ErrObjectChanged
	}

	var body io.Reader = bytes.NewReader(obj.data)
	if limit, broken := o.breakAt[key]; broken && limit < len(obj.data) {
		body = io.MultiReader(io.LimitReader(body, int64(limit)), errReader{})
	}

	return segmentadmin.Object{Body: io.NopCloser(body), Size: int64(len(obj.data)), Gzip: obj.gzip}, nil
}

func (o *Objects) fail() error {
	if o.headFails > 0 {
		o.headFails--

		return ErrReadFailed
	}

	return nil
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, ErrReadFailed }
