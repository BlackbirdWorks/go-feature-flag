package s3publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// job is one distinct key to make present.
type job struct {
	item   *planned
	status Status
}

// ensureObjects makes every object exist under its key, each distinct key once, with at most Concurrency calls at
// a time. The statuses are recorded in the order of the manifest whatever the order of completion. The first
// error cancels the calls still waiting and is returned.
func (p *Publisher) ensureObjects(ctx context.Context, items []planned, dryRun bool, res *Result) error {
	jobs := make([]*job, 0, len(items))
	byKey := make(map[string]*job, len(items))

	for i := range items {
		if _, dup := byKey[items[i].entry.Key]; dup {
			continue
		}

		jb := &job{item: &items[i]}
		byKey[items[i].entry.Key] = jb
		jobs = append(jobs, jb)
	}

	err := p.parallel(ctx, len(jobs), func(ctx context.Context, i int) error {
		status, err := p.ensure(ctx, jobs[i].item, dryRun)
		jobs[i].status = status

		return err
	})
	if err != nil {
		return err
	}

	for _, jb := range jobs {
		if jb.status == StatusPresent {
			res.Present++
		} else {
			res.Uploaded++
			res.UploadedBytes += jb.item.entry.Size
		}
	}

	for i := range items {
		res.Objects = append(res.Objects, ObjectResult{
			Name: items[i].entry.Name, Key: items[i].entry.Key, Size: items[i].entry.Size,
			Status: byKey[items[i].entry.Key].status,
		})
	}

	return nil
}

// parallel runs call for 0..n-1 with at most Concurrency running at once. The first error cancels the context of the
// calls and stops the start of new ones. The error returned is the one of the lowest index that is not just the
// consequence of the cancellation.
func (p *Publisher) parallel(parent context.Context, count int, call func(ctx context.Context, i int) error) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	var wg sync.WaitGroup

	slots := make(chan struct{}, p.cfg.Concurrency)
	errs := make([]error, count)

start:
	for i := range count {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break start
		}

		wg.Go(func() {
			defer func() { <-slots }()

			err := call(ctx, i)
			if err != nil {
				errs[i] = err

				cancel()
			}
		})
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
	}

	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	err := parent.Err()
	if err != nil {
		return fmt.Errorf("publish: %w", err)
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

// exists runs HeadObject and checks that an existing object is the expected one. The size must match. A full
// sha256 reported by the store must match. When the store reports none, or a composite checksum of a multipart
// upload (which is not the sha256 of the bytes), the object is read and hashed: it is never accepted on its size
// alone.
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

	sum := aws.ToString(out.ChecksumSHA256)
	if sum != "" && !strings.Contains(sum, "-") {
		if sum != item.sha64 {
			return false, fmt.Errorf("%w: %s has another sha256", ErrObjectMismatch, item.entry.Key)
		}

		return true, nil
	}

	return p.hashMatches(ctx, item)
}

// hashMatches reads the object and compares its sha256 with the expected one.
func (p *Publisher) hashMatches(ctx context.Context, item *planned) (bool, error) {
	body, _, err := p.read(ctx, item.entry.Key, item.entry.Size)

	switch {
	case isNotFound(err):
		return false, nil
	case errors.Is(err, errTooLarge):
		return false, fmt.Errorf("%w: %s is larger than %d bytes", ErrObjectMismatch, item.entry.Key, item.entry.Size)
	case err != nil:
		return false, err
	}

	raw := sha256.Sum256(body)
	if base64.StdEncoding.EncodeToString(raw[:]) != item.sha64 {
		return false, fmt.Errorf("%w: %s has another sha256", ErrObjectMismatch, item.entry.Key)
	}

	return true, nil
}
