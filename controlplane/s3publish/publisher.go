package s3publish

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// anyETag is the value of If-None-Match that makes a write create-only.
const anyETag = "*"

// The operations named in an OperationError.
const (
	opGet  = "GetObject"
	opHead = "HeadObject"
	opPut  = "PutObject"
)

// API is the part of the S3 client that the publisher uses: three calls, no LIST and no DELETE. A *s3.Client
// implements it.
type API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// counter counts the calls that pass through it. It serves one Publish or Verify, whose object calls may run
// concurrently.
type counter struct {
	API

	get, head, put atomic.Int64
}

func (c *counter) GetObject(
	ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	c.get.Add(1)

	return c.API.GetObject(ctx, in, opts...) //nolint:wrapcheck // the caller classifies and wraps the client error
}

func (c *counter) HeadObject(
	ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	c.head.Add(1)

	return c.API.HeadObject(ctx, in, opts...) //nolint:wrapcheck // the caller classifies and wraps the client error
}

func (c *counter) PutObject(
	ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	c.put.Add(1)

	return c.API.PutObject(ctx, in, opts...) //nolint:wrapcheck // the caller classifies and wraps the client error
}

func (c *counter) requests() Requests {
	return Requests{Get: int(c.get.Load()), Head: int(c.head.Load()), Put: int(c.put.Load())}
}

// Publisher publishes to one bucket and channel. It holds no mutable state: it is safe for concurrent use and
// two publishers (or two processes) on one bucket are what the conditional writes protect against.
type Publisher struct {
	api API
	cfg Config
}

// New returns a Publisher. It makes no S3 call. cfg is copied and its zero limits get their defaults.
func New(api API, cfg *Config) (*Publisher, error) {
	if api == nil || cfg == nil {
		return nil, fmt.Errorf("%w: no client or no configuration", ErrConfig)
	}

	conf := *cfg

	err := conf.validate()
	if err != nil {
		return nil, err
	}

	if conf.Prefix != "" && !strings.HasSuffix(conf.Prefix, "/") {
		conf.Prefix += "/"
	}

	if conf.ManifestKey == "" {
		conf.ManifestKey = conf.Prefix + "manifests/" + conf.Channel + ".yaml"
	}

	conf.MaxObjects = defaultInt(conf.MaxObjects, controlplane.DefaultMaxObjects)
	conf.Concurrency = defaultInt(conf.Concurrency, DefaultConcurrency)
	conf.MaxObjectBytes = defaultBytes(conf.MaxObjectBytes, controlplane.DefaultMaxObjectBytes)
	conf.MaxTotalBytes = defaultBytes(conf.MaxTotalBytes, controlplane.DefaultMaxTotalBytes)
	conf.MaxManifestBytes = defaultBytes(conf.MaxManifestBytes, controlplane.DefaultMaxManifestBytes)

	return &Publisher{api: api, cfg: conf}, nil
}

// validate checks what New cannot repair.
func (c *Config) validate() error {
	if c.Bucket == "" || c.Channel == "" || c.Connection == "" {
		return fmt.Errorf("%w: bucket, channel and connection are required", ErrConfig)
	}

	if strings.HasPrefix(c.Prefix, "/") {
		return fmt.Errorf("%w: prefix must not start with a slash", ErrConfig)
	}

	if c.MaxObjects < 0 || c.MaxObjectBytes < 0 || c.MaxTotalBytes < 0 || c.MaxManifestBytes < 0 || c.Concurrency < 0 {
		return fmt.Errorf("%w: limits must not be negative", ErrConfig)
	}

	return nil
}

func defaultInt(value, def int) int {
	if value == 0 {
		return def
	}

	return value
}

func defaultBytes(value, def int64) int64 {
	if value == 0 {
		return def
	}

	return value
}

// ManifestKey returns the key of the manifest.
func (p *Publisher) ManifestKey() string { return p.cfg.ManifestKey }

// planned is an object with its manifest entry, ready to upload.
type planned struct {
	entry  *controlplane.ManifestObject
	source string
	format string
	sha64  string
	body   []byte
}

// plan is a validated request: the manifest to write (generation not set yet) and the objects it lists.
type plan struct {
	manifest *controlplane.Manifest
	items    []planned
	warnings []string
}

// current is the manifest found in the bucket. manifest is nil when it could not be used and the caller asked to
// replace it (invalid says why).
type current struct {
	manifest *controlplane.Manifest
	invalid  error
	etag     string
}

// Publish uploads the objects that are absent, then writes the manifest, as described in the package comment.
// Nothing is written when validation fails, when the bucket already holds this exact content (a listed object
// that is missing is uploaded again), or with DryRun. Repeating a failed Publish is safe: objects are immutable
// and unreferenced until the manifest is written.
func (p *Publisher) Publish(ctx context.Context, req Request) (*Result, error) {
	counted := &counter{API: p.api}

	res, err := (&Publisher{api: counted, cfg: p.cfg}).run(ctx, &req)
	if res != nil {
		res.Requests = counted.requests()
	}

	return res, err
}

func (p *Publisher) run(ctx context.Context, req *Request) (*Result, error) {
	prep, err := p.plan(req)
	if err != nil {
		return nil, err
	}

	cur, err := p.readManifest(ctx, req.ReplaceInvalidManifest)
	if err != nil {
		return nil, err
	}

	res := &Result{
		Channel: p.cfg.Channel, ManifestKey: p.cfg.ManifestKey, DryRun: req.DryRun, Warnings: prep.warnings,
		Objects: make([]ObjectResult, 0, len(prep.items)),
	}

	generation, same, err := nextGeneration(cur, prep.manifest.Objects, req.Generation)
	if err != nil {
		return nil, err
	}

	res.Generation = generation

	if cur != nil && cur.manifest != nil {
		res.Previous = cur.manifest.Generation
	}

	if same {
		// The manifest is current: write nothing, but make sure that what it lists is there, and heal a
		// missing object.
		res.ManifestETag = cur.etag

		err = p.ensureObjects(ctx, prep.items, req.DryRun, res)
		if err != nil {
			return nil, err
		}

		return res, nil
	}

	res.Changed = true
	prep.manifest.Generation = generation

	err = p.ensureObjects(ctx, prep.items, req.DryRun, res)
	if err != nil {
		return nil, err
	}

	if req.DryRun {
		return res, nil
	}

	etag, err := p.writeManifest(ctx, prep.manifest, cur)
	if err != nil {
		return nil, err
	}

	res.ManifestETag = etag
	res.Written = true

	return res, nil
}

// nextGeneration decides the generation to write and whether the bucket already holds the content.
func nextGeneration(cur *current, objects []controlplane.ManifestObject, requested uint64) (uint64, bool, error) {
	if cur == nil {
		return max(requested, 1), false, nil
	}

	if cur.manifest == nil {
		if requested == 0 {
			return 0, false, fmt.Errorf("%w: replacing an invalid manifest needs an explicit generation", ErrGeneration)
		}

		return requested, false, nil
	}

	prev := cur.manifest.Generation
	same := slices.Equal(cur.manifest.Objects, objects)

	switch {
	case requested == 0 && same:
		return prev, true, nil
	case requested == 0 && prev == math.MaxUint64:
		return 0, false, fmt.Errorf("%w: the bucket holds the largest generation", ErrGeneration)
	case requested == 0:
		return prev + 1, false, nil
	case requested == prev && same:
		return prev, true, nil
	case requested <= prev:
		return 0, false, fmt.Errorf("%w: requested %d, the bucket holds %d", ErrGeneration, requested, prev)
	}

	return requested, false, nil
}

// decodeManifest reads and validates the bytes of a manifest, and checks it against the current limits.
func (p *Publisher) decodeManifest(body []byte) (*controlplane.Manifest, error) {
	manifest, _, err := controlplane.UnmarshalManifest(body)
	if err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}

	err = controlplane.ValidateManifest(manifest, controlplane.WithMaxObjects(p.cfg.MaxObjects))
	if err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}

	err = p.checkLimits(manifest.Objects, func(i int) string { return manifest.Objects[i].Name }, false)
	if err != nil {
		return nil, err
	}

	slices.SortStableFunc(manifest.Objects, func(a, b controlplane.ManifestObject) int {
		return strings.Compare(a.Name, b.Name)
	})

	return manifest, nil
}

// checkLimits applies the limits of the reader to the entries of a manifest: the sum over the distinct checksums
// is at most MaxTotalBytes and, when strict (a manifest about to be written), every size is between 1 and
// MaxObjectBytes. The reader fails an entry of another size alone (it is never requested), so for a manifest that is
// already in the bucket such an entry is not a problem of the manifest. label names entry i.
func (p *Publisher) checkLimits(objects []controlplane.ManifestObject, label func(i int) string, strict bool) error {
	var total int64

	seen := make(map[string]struct{}, len(objects))

	for i := range objects {
		size := objects[i].Size

		switch {
		case size < 1 && strict:
			return fmt.Errorf("%w: %s: size %d, an object must not be empty", ErrLimit, label(i), size)
		case size > p.cfg.MaxObjectBytes && strict:
			return fmt.Errorf("%w: %s: size %d bytes is over the limit of %d bytes",
				ErrLimit, label(i), size, p.cfg.MaxObjectBytes)
		case size < 1 || size > p.cfg.MaxObjectBytes:
			// An entry of a manifest that is already published and that the reader fails on its own: it is
			// never requested and counts for nothing, so it is not a problem of the manifest.
			continue
		}

		if _, dup := seen[objects[i].Checksum.Value]; dup {
			continue
		}

		seen[objects[i].Checksum.Value] = struct{}{}

		if total > math.MaxInt64-size {
			total = math.MaxInt64
		} else {
			total += size
		}
	}

	if total > p.cfg.MaxTotalBytes {
		return fmt.Errorf(
			"%w: objects total %d bytes, over the limit of %d bytes",
			ErrLimit,
			total,
			p.cfg.MaxTotalBytes,
		)
	}

	return nil
}

// marshalManifest writes the manifest as YAML, or as JSON when the manifest key ends in ".json".
func (p *Publisher) marshalManifest(manifest *controlplane.Manifest) ([]byte, string, error) {
	if strings.HasSuffix(p.cfg.ManifestKey, ".json") {
		body, err := controlplane.MarshalManifestJSON(manifest)

		return body, controlplane.FormatJSON, err //nolint:wrapcheck // wrapped by the callers
	}

	body, err := controlplane.MarshalManifest(manifest)

	return body, controlplane.FormatYAML, err //nolint:wrapcheck // wrapped by the callers
}

// readManifest reads the manifest in the bucket: nil when there is none. A manifest that cannot be used is
// ErrPreviousManifest, or with replace a current without manifest that carries only the ETag.
func (p *Publisher) readManifest(ctx context.Context, replace bool) (*current, error) {
	body, etag, err := p.read(ctx, p.cfg.ManifestKey, "", p.cfg.MaxManifestBytes)
	if isNotFound(err) {
		return nil, nil //nolint:nilnil // no manifest is a state, not an error
	}

	var decodeErr error

	switch {
	case errors.Is(err, errTooLarge):
		decodeErr = fmt.Errorf("%w: the manifest is over the limit of %d bytes", ErrLimit, p.cfg.MaxManifestBytes)
	case err != nil:
		return nil, err
	}

	var manifest *controlplane.Manifest

	if decodeErr == nil {
		manifest, decodeErr = p.decodeManifest(body)
	}

	if decodeErr != nil {
		if replace {
			return &current{etag: etag, invalid: decodeErr}, nil
		}

		return nil, fmt.Errorf("%w: %s: %w; to replace it set Request.ReplaceInvalidManifest "+
			"(CLI: --replace-invalid-manifest) together with an explicit generation",
			ErrPreviousManifest, p.cfg.ManifestKey, decodeErr)
	}

	if manifest.Channel != p.cfg.Channel {
		return nil, fmt.Errorf("%w: %q, want %q", ErrChannelMismatch, manifest.Channel, p.cfg.Channel)
	}

	return &current{manifest: manifest, etag: etag}, nil
}

var (
	errTooLarge       = errors.New("object is larger than the limit")
	errMissingVersion = errors.New("the pinned object version does not exist")
)

// read gets an object and returns its bytes (at most limit) and ETag. A missing key is an error that
// isNotFound recognises; any other failure is an *OperationError. An object over the limit is an
// *OperationError that wraps errTooLarge, with the ETag still returned.
func (p *Publisher) read(ctx context.Context, key, versionID string, limit int64) ([]byte, string, error) {
	input := &s3.GetObjectInput{Bucket: aws.String(p.cfg.Bucket), Key: aws.String(key)}
	if versionID != "" {
		input.VersionId = aws.String(versionID)
	}

	out, err := p.api.GetObject(ctx, input)
	if err != nil {
		if versionID != "" && errorCode(err) == "InvalidArgument" {
			// AWS answers 400 InvalidArgument for a version id that does not exist.
			return nil, "", fmt.Errorf("get %s version %s: %w: %w", key, versionID, errMissingVersion, err)
		}

		if isNotFound(err) {
			return nil, "", fmt.Errorf("get %s: %w", key, err)
		}

		return nil, "", &OperationError{Operation: opGet, Key: key, Err: err}
	}

	defer func() { _ = out.Body.Close() }()

	etag := aws.ToString(out.ETag)

	// One byte more than the limit tells that the object is over it. The bound is not limit+1 when that would wrap.
	bound := limit
	if limit < math.MaxInt64 {
		bound = limit + 1
	}

	body, err := io.ReadAll(io.LimitReader(out.Body, bound))
	if err != nil {
		return nil, etag, &OperationError{Operation: opGet, Key: key, Err: err}
	}

	if int64(len(body)) > limit {
		return nil, etag, &OperationError{Operation: opGet, Key: key, Err: errTooLarge}
	}

	return body, etag, nil
}

// writeManifest writes the manifest last, create-only when there was none and If-Match the ETag read otherwise.
// A failed condition is resolved by one read: when the manifest in the bucket is the one we tried to write, an
// earlier attempt of ours committed (the SDK retried a request whose answer was lost) and that is success.
func (p *Publisher) writeManifest(ctx context.Context, manifest *controlplane.Manifest, cur *current) (string, error) {
	body, format, err := p.marshalManifest(manifest)
	if err != nil {
		return "", invalid("manifest", err)
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(p.cfg.Bucket), Key: aws.String(p.cfg.ManifestKey), Body: bytes.NewReader(body),
		ContentType: aws.String(contentType(format)),
	}
	if cur == nil {
		input.IfNoneMatch = aws.String(anyETag)
	} else {
		input.IfMatch = aws.String(cur.etag)
	}

	out, err := p.api.PutObject(ctx, input)
	if isPrecondition(err) || (cur != nil && isNotFound(err)) {
		return p.resolveConflict(ctx, manifest)
	}

	if err != nil {
		return "", &OperationError{Operation: opPut, Key: p.cfg.ManifestKey, Err: err}
	}

	return aws.ToString(out.ETag), nil
}

// resolveConflict reads the manifest after a failed write and returns its ETag when it is ours, else ErrConflict.
func (p *Publisher) resolveConflict(ctx context.Context, written *controlplane.Manifest) (string, error) {
	conflict := fmt.Errorf("%w: %s", ErrConflict, p.cfg.ManifestKey)

	body, etag, err := p.read(ctx, p.cfg.ManifestKey, "", p.cfg.MaxManifestBytes)

	switch {
	case isNotFound(err), errors.Is(err, errTooLarge):
		return "", conflict
	case err != nil:
		return "", err
	}

	found, err := p.decodeManifest(body)
	if err != nil {
		return "", conflict
	}

	if p.sameManifest(found, written) {
		return etag, nil
	}

	return "", conflict
}

// sameManifest reports whether the manifest in the bucket is the whole manifest we tried to write: the same
// channel, generation and entries, and the same createdAt (both absent, or the same instant). The manifest we wrote
// is compared as it reads back, so the form it was serialised in does not matter.
func (p *Publisher) sameManifest(found, written *controlplane.Manifest) bool {
	body, _, err := p.marshalManifest(written)
	if err != nil {
		return false
	}

	ours, _, err := controlplane.UnmarshalManifest(body)
	if err != nil {
		return false
	}

	slices.SortStableFunc(
		ours.Objects,
		func(a, b controlplane.ManifestObject) int { return strings.Compare(a.Name, b.Name) },
	)

	return found.Channel == ours.Channel && found.Generation == ours.Generation &&
		slices.Equal(found.Objects, ours.Objects) && sameInstant(found.CreatedAt, ours.CreatedAt)
}

// sameInstant compares two optional times: absent equals absent, and times are equal by instant.
func sameInstant(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return a.Equal(*b)
}

func contentType(format string) string {
	if format == controlplane.FormatJSON {
		return "application/json"
	}

	return "application/yaml"
}
