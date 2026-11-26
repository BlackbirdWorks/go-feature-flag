package s3publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// maxManifestBytes bounds the manifest read into memory (a manifest of DefaultMaxObjects entries is a few MiB).
const maxManifestBytes = 64 << 20

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

// counter counts the calls that pass through it. It serves one sequential Publish or Verify.
type counter struct {
	API

	n Requests
}

func (c *counter) GetObject(
	ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	c.n.Get++

	return c.API.GetObject(ctx, in, opts...) //nolint:wrapcheck // the caller classifies and wraps the client error
}

func (c *counter) HeadObject(
	ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	c.n.Head++

	return c.API.HeadObject(ctx, in, opts...) //nolint:wrapcheck // the caller classifies and wraps the client error
}

func (c *counter) PutObject(
	ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	c.n.Put++

	return c.API.PutObject(ctx, in, opts...) //nolint:wrapcheck // the caller classifies and wraps the client error
}

// Publisher publishes to one bucket and channel. It holds no mutable state: it is safe for concurrent use and
// two publishers (or two processes) on one bucket are what the conditional writes protect against.
type Publisher struct {
	api API
	cfg Config
}

// New returns a Publisher. It makes no S3 call. cfg is copied.
func New(api API, cfg *Config) (*Publisher, error) {
	if api == nil || cfg == nil {
		return nil, fmt.Errorf("%w: no client or no configuration", ErrConfig)
	}

	conf := *cfg

	if conf.Bucket == "" || conf.Channel == "" || conf.Connection == "" {
		return nil, fmt.Errorf("%w: bucket, channel and connection are required", ErrConfig)
	}

	if strings.HasPrefix(conf.Prefix, "/") {
		return nil, fmt.Errorf("%w: prefix must not start with a slash", ErrConfig)
	}

	if conf.Prefix != "" && !strings.HasSuffix(conf.Prefix, "/") {
		conf.Prefix += "/"
	}

	if conf.ManifestKey == "" {
		conf.ManifestKey = conf.Prefix + "manifests/" + conf.Channel + ".yaml"
	}

	return &Publisher{api: api, cfg: conf}, nil
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

// current is the manifest found in the bucket.
type current struct {
	manifest *controlplane.Manifest
	etag     string
}

// Publish uploads the objects that are absent, then writes the manifest, as described in the package comment.
// Nothing is written when validation fails, when the bucket already holds this exact content, or with DryRun.
// Repeating a failed Publish is safe: objects are immutable and unreferenced until the manifest is written.
func (p *Publisher) Publish(ctx context.Context, req Request) (*Result, error) {
	counted := &counter{API: p.api}

	res, err := (&Publisher{api: counted, cfg: p.cfg}).run(ctx, &req)
	if res != nil {
		res.Requests = counted.n
	}

	return res, err
}

func (p *Publisher) run(ctx context.Context, req *Request) (*Result, error) {
	prep, err := p.plan(req)
	if err != nil {
		return nil, err
	}

	cur, err := p.readManifest(ctx)
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

	if cur != nil {
		res.Previous = cur.manifest.Generation
	}

	if same {
		res.ManifestETag = cur.etag
		res.Objects = unchanged(cur.manifest.Objects)

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

// unchanged lists the entries of a manifest that is already in the bucket.
func unchanged(objects []controlplane.ManifestObject) []ObjectResult {
	out := make([]ObjectResult, 0, len(objects))
	for i := range objects {
		out = append(out, ObjectResult{
			Name: objects[i].Name, Key: objects[i].Key, Size: objects[i].Size, Status: StatusUnchanged,
		})
	}

	return out
}

// nextGeneration decides the generation to write and whether the bucket already holds the content.
func nextGeneration(cur *current, objects []controlplane.ManifestObject, requested uint64) (uint64, bool, error) {
	if cur == nil {
		return max(requested, 1), false, nil
	}

	prev := cur.manifest.Generation
	same := slices.Equal(cur.manifest.Objects, objects)

	switch {
	case requested == 0 && same:
		return prev, true, nil
	case requested == 0:
		return prev + 1, false, nil
	case requested == prev && same:
		return prev, true, nil
	case requested <= prev:
		return 0, false, fmt.Errorf("%w: requested %d, the bucket holds %d", ErrGeneration, requested, prev)
	}

	return requested, false, nil
}

// plan validates the request and builds the manifest. It makes no S3 call.
func (p *Publisher) plan(req *Request) (*plan, error) {
	items := make([]planned, 0, len(req.Objects))

	for i := range req.Objects {
		item, err := p.plannedObject(&req.Objects[i])
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	slices.SortStableFunc(items, func(a, b planned) int { return strings.Compare(a.entry.Name, b.entry.Name) })

	manifest := &controlplane.Manifest{
		APIVersion: controlplane.APIVersion, Kind: controlplane.DocumentKindManifest, Channel: p.cfg.Channel,
		Generation: 1, CreatedAt: req.CreatedAt, Objects: make([]controlplane.ManifestObject, len(items)),
	}
	for i := range items {
		manifest.Objects[i] = *items[i].entry
	}

	err := controlplane.ValidateManifest(manifest, controlplane.WithMaxObjects(p.cfg.MaxObjects))
	if err != nil {
		return nil, invalid("manifest", err)
	}

	warnings, err := validateContents(items, manifest)
	if err != nil {
		return nil, err
	}

	return &plan{manifest: manifest, items: items, warnings: warnings}, nil
}

// plannedObject computes the checksum, key and manifest entry of one object.
func (p *Publisher) plannedObject(obj *Object) (planned, error) {
	name := label(obj.Name, obj.Source)

	if obj.Format != controlplane.FormatYAML && obj.Format != controlplane.FormatJSON {
		return planned{}, invalid(
			name,
			fmt.Errorf("%w: %q (use yaml or json)", controlplane.ErrUnsupportedFormat, obj.Format),
		)
	}

	sum := controlplane.ChecksumOf(obj.Body)

	key, err := controlplane.ObjectKey(sum, obj.Format)
	if err != nil {
		return planned{}, invalid(name, err)
	}

	raw := sha256.Sum256(obj.Body)

	return planned{
		source: obj.Source, body: obj.Body, format: obj.Format, sha64: base64.StdEncoding.EncodeToString(raw[:]),
		entry: &controlplane.ManifestObject{
			Name: obj.Name, Kind: obj.Kind, Connection: p.cfg.Connection, Key: p.cfg.Prefix + key,
			Format: obj.Format, Checksum: sum, Size: int64(len(obj.Body)),
		},
	}, nil
}

// validateContents decodes every document and runs the Validate functions of the controlplane package.
func validateContents(items []planned, manifest *controlplane.Manifest) ([]string, error) {
	var warnings []string

	for i := range items {
		info, err := validateContent(items[i].entry, items[i].body, manifest)
		if err != nil {
			return nil, invalid(label(items[i].entry.Name, items[i].source), err)
		}

		for _, field := range info.UnknownFields {
			warnings = append(warnings, fmt.Sprintf("object %s: unknown field %s", items[i].entry.Name, field))
		}
	}

	return warnings, nil
}

// validateContent validates the bytes of one object by its kind. manifest may be nil: source references are
// then not resolved. The errors are the ones of the controlplane package.
func validateContent(
	entry *controlplane.ManifestObject, body []byte, manifest *controlplane.Manifest,
) (controlplane.DecodeInfo, error) {
	switch entry.Kind {
	case controlplane.KindFlags:
		flags, info, err := controlplane.UnmarshalFlags(body, entry.Format)
		if err != nil {
			return info, fmt.Errorf("decode flags: %w", err)
		}

		err = controlplane.ValidateFlags(flags)
		if err != nil {
			return info, fmt.Errorf("validate flags: %w", err)
		}

		return info, nil
	case controlplane.KindFlagSets:
		doc, info, err := controlplane.UnmarshalFlagSet(body)
		if err != nil {
			return info, fmt.Errorf("decode flag sets: %w", err)
		}

		err = controlplane.ValidateFlagSet(doc, manifest)
		if err != nil {
			return info, fmt.Errorf("validate flag sets: %w", err)
		}

		return info, nil
	}

	return controlplane.DecodeInfo{}, fmt.Errorf("%w: kind %q", controlplane.ErrInvalid, entry.Kind)
}

// label names an object in an error: its name and, when known, where it came from.
func label(name, source string) string {
	if source == "" {
		return name
	}

	return name + " (" + source + ")"
}

func invalid(name string, err error) error {
	return fmt.Errorf("%w: %s: %w", ErrInvalid, name, err)
}

// decodeManifest reads and validates the bytes of a manifest.
func (p *Publisher) decodeManifest(body []byte) (*controlplane.Manifest, error) {
	manifest, _, err := controlplane.UnmarshalManifest(body)
	if err != nil {
		return nil, fmt.Errorf("decode manifest: %w", err)
	}

	err = controlplane.ValidateManifest(manifest, controlplane.WithMaxObjects(p.cfg.MaxObjects))
	if err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}

	slices.SortStableFunc(manifest.Objects, func(a, b controlplane.ManifestObject) int {
		return strings.Compare(a.Name, b.Name)
	})

	return manifest, nil
}

// readManifest reads the manifest in the bucket: nil when there is none.
func (p *Publisher) readManifest(ctx context.Context) (*current, error) {
	body, etag, err := p.read(ctx, p.cfg.ManifestKey, maxManifestBytes)
	if isNotFound(err) {
		return nil, nil //nolint:nilnil // no manifest is a state, not an error
	}

	if err != nil {
		return nil, err
	}

	manifest, err := p.decodeManifest(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrPreviousManifest, p.cfg.ManifestKey, err)
	}

	if manifest.Channel != p.cfg.Channel {
		return nil, fmt.Errorf("%w: %q, want %q", ErrChannelMismatch, manifest.Channel, p.cfg.Channel)
	}

	return &current{manifest: manifest, etag: etag}, nil
}

var errTooLarge = errors.New("object is larger than the limit")

// read gets an object and returns its bytes (at most limit) and ETag. A missing key is an error that
// isNotFound recognises; any other failure is an *OperationError.
func (p *Publisher) read(ctx context.Context, key string, limit int64) ([]byte, string, error) {
	out, err := p.api.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(p.cfg.Bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return nil, "", fmt.Errorf("get %s: %w", key, err)
		}

		return nil, "", &OperationError{Operation: opGet, Key: key, Err: err}
	}

	defer func() { _ = out.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(out.Body, limit+1))
	if err != nil {
		return nil, "", &OperationError{Operation: opGet, Key: key, Err: err}
	}

	if int64(len(body)) > limit {
		return nil, "", &OperationError{Operation: opGet, Key: key, Err: errTooLarge}
	}

	return body, aws.ToString(out.ETag), nil
}

// ensureObjects creates the objects that are absent, each key once.
func (p *Publisher) ensureObjects(ctx context.Context, items []planned, dryRun bool, res *Result) error {
	done := make(map[string]Status, len(items))

	for i := range items {
		item := &items[i]

		status, seen := done[item.entry.Key]
		if !seen {
			var err error

			status, err = p.ensure(ctx, item, dryRun)
			if err != nil {
				return err
			}

			done[item.entry.Key] = status

			if status == StatusPresent {
				res.Present++
			} else {
				res.Uploaded++
				res.UploadedBytes += item.entry.Size
			}
		}

		res.Objects = append(res.Objects, ObjectResult{
			Name: item.entry.Name, Key: item.entry.Key, Size: item.entry.Size, Status: status,
		})
	}

	return nil
}

// ensure makes the object exist under its key: HeadObject, then a create-only PutObject when it is absent.
func (p *Publisher) ensure(ctx context.Context, item *planned, dryRun bool) (Status, error) {
	found, err := p.exists(ctx, item)
	if err != nil {
		return "", err
	}

	if found {
		return StatusPresent, nil
	}

	if dryRun {
		return StatusWouldUpload, nil
	}

	_, err = p.api.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(p.cfg.Bucket), Key: aws.String(item.entry.Key), Body: bytes.NewReader(item.body),
		ContentType: aws.String(contentType(item.format)), IfNoneMatch: aws.String(anyETag),
		ChecksumSHA256: aws.String(item.sha64),
	})
	if isPrecondition(err) {
		// Created by someone else after our HeadObject: the key is content addressed, so check and accept.
		found, err = p.exists(ctx, item)
		if err == nil && !found {
			err = fmt.Errorf("%w: %s", ErrConflict, item.entry.Key)
		}

		if err != nil {
			return "", err
		}

		return StatusPresent, nil
	}

	if err != nil {
		return "", &OperationError{Operation: opPut, Key: item.entry.Key, Err: err}
	}

	return StatusUploaded, nil
}

// exists runs HeadObject and checks that an existing object has the expected size and, when the store reports
// it, the expected sha256.
func (p *Publisher) exists(ctx context.Context, item *planned) (bool, error) {
	out, err := p.api.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(p.cfg.Bucket), Key: aws.String(item.entry.Key), ChecksumMode: types.ChecksumModeEnabled,
	})
	if isNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, &OperationError{Operation: opHead, Key: item.entry.Key, Err: err}
	}

	if size := aws.ToInt64(out.ContentLength); size != item.entry.Size {
		return false, fmt.Errorf(
			"%w: %s has %d bytes, want %d",
			ErrObjectMismatch,
			item.entry.Key,
			size,
			item.entry.Size,
		)
	}

	if sum := aws.ToString(out.ChecksumSHA256); sum != "" && sum != item.sha64 {
		return false, fmt.Errorf("%w: %s has another sha256", ErrObjectMismatch, item.entry.Key)
	}

	return true, nil
}

// writeManifest writes the manifest last, create-only when there was none and If-Match the ETag read otherwise.
func (p *Publisher) writeManifest(ctx context.Context, manifest *controlplane.Manifest, cur *current) (string, error) {
	var (
		body   []byte
		err    error
		format = controlplane.FormatYAML
	)

	if strings.HasSuffix(p.cfg.ManifestKey, ".json") {
		format = controlplane.FormatJSON
		body, err = controlplane.MarshalManifestJSON(manifest)
	} else {
		body, err = controlplane.MarshalManifest(manifest)
	}

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
		return "", fmt.Errorf("%w: %s", ErrConflict, p.cfg.ManifestKey)
	}

	if err != nil {
		return "", &OperationError{Operation: opPut, Key: p.cfg.ManifestKey, Err: err}
	}

	return aws.ToString(out.ETag), nil
}

func contentType(format string) string {
	if format == controlplane.FormatJSON {
		return "application/json"
	}

	return "application/yaml"
}
