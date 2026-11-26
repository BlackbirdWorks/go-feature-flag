package s3publish

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// Codes of a Problem.
const (
	// ProblemManifestInvalid: the manifest cannot be decoded or fails validation.
	ProblemManifestInvalid = "manifest-invalid"
	// ProblemMissing: the object listed in the manifest does not exist.
	ProblemMissing = "missing"
	// ProblemUnreadable: the object could not be read (access, transport).
	ProblemUnreadable = "unreadable"
	// ProblemSizeMismatch: the stored size is not the listed size.
	ProblemSizeMismatch = "size-mismatch"
	// ProblemChecksumMismatch: the stored bytes do not have the listed checksum.
	ProblemChecksumMismatch = "checksum-mismatch"
	// ProblemInvalidContent: the object does not decode or fails validation for its kind.
	ProblemInvalidContent = "invalid-content"
)

// Problem is one thing wrong in the bucket.
type Problem struct {
	// Object is the manifest name of the object ("manifest" for the manifest itself).
	Object string
	// Key is the object key.
	Key string
	// Code is one of the Problem constants, or the controlplane Code of a validation problem.
	Code string
	// Detail says more. It never contains the content of a document, so no API key.
	Detail string
}

// Report is the outcome of Verify.
type Report struct {
	Channel      string
	ManifestKey  string
	ManifestETag string
	// Problems lists what is wrong, in manifest order.
	Problems []Problem
	// Skipped lists the objects of other connections: this client reads one bucket only.
	Skipped []string
	// Requests counts the S3 calls this Verify made.
	Requests   Requests
	Generation uint64
	// Checked counts the objects read and compared.
	Checked int
}

// OK reports that no problem was found.
func (r *Report) OK() bool { return len(r.Problems) == 0 }

// Verify reads the manifest and every listed object of the connection, and checks size, checksum, the content of
// each document by its kind, and that every flag-set source resolves in the manifest. It never writes. The error
// is non-nil only when the manifest cannot be read at all (ErrNoManifest, *OperationError); everything wrong
// with the content is in the report.
func (p *Publisher) Verify(ctx context.Context) (*Report, error) {
	counted := &counter{API: p.api}

	rep, err := (&Publisher{api: counted, cfg: p.cfg}).check(ctx)
	if rep != nil {
		rep.Requests = counted.n
	}

	return rep, err
}

func (p *Publisher) check(ctx context.Context) (*Report, error) {
	body, etag, err := p.read(ctx, p.cfg.ManifestKey, maxManifestBytes)
	if isNotFound(err) {
		return nil, fmt.Errorf("%w: %s", ErrNoManifest, p.cfg.ManifestKey)
	}

	if err != nil {
		return nil, err
	}

	rep := &Report{Channel: p.cfg.Channel, ManifestKey: p.cfg.ManifestKey, ManifestETag: etag}

	manifest, decodeErr := p.decodeManifest(body)
	if decodeErr != nil {
		rep.Problems = append(rep.Problems, p.manifestProblem(decodeErr.Error()))

		return rep, nil
	}

	rep.Generation = manifest.Generation

	if manifest.Channel != p.cfg.Channel {
		rep.Problems = append(rep.Problems, p.manifestProblem(
			fmt.Sprintf("channel %q, want %q", manifest.Channel, p.cfg.Channel)))
	}

	for i := range manifest.Objects {
		obj := &manifest.Objects[i]

		if obj.Connection != p.cfg.Connection {
			rep.Skipped = append(rep.Skipped, obj.Name)

			continue
		}

		rep.Checked++
		rep.Problems = append(rep.Problems, p.verifyObject(ctx, obj, manifest)...)
	}

	return rep, nil
}

func (p *Publisher) manifestProblem(detail string) Problem {
	return Problem{Object: "manifest", Key: p.cfg.ManifestKey, Code: ProblemManifestInvalid, Detail: detail}
}

// verifyObject checks one object and returns its problems.
func (p *Publisher) verifyObject(
	ctx context.Context, obj *controlplane.ManifestObject, manifest *controlplane.Manifest,
) []Problem {
	problem := func(code, detail string) []Problem {
		return []Problem{{Object: obj.Name, Key: obj.Key, Code: code, Detail: detail}}
	}

	body, _, err := p.read(ctx, obj.Key, max(obj.Size, 0))

	var opErr *OperationError

	switch {
	case isNotFound(err):
		return problem(ProblemMissing, "no object under the listed key")
	case errors.As(err, &opErr) && errors.Is(opErr.Err, errTooLarge):
		return problem(ProblemSizeMismatch, fmt.Sprintf("larger than the listed %d bytes", obj.Size))
	case err != nil:
		return problem(ProblemUnreadable, err.Error())
	}

	if int64(len(body)) != obj.Size {
		return problem(ProblemSizeMismatch, fmt.Sprintf("%d bytes, listed %d", len(body), obj.Size))
	}

	err = controlplane.VerifyChecksum(body, obj.Checksum)
	if err != nil {
		return problem(ProblemChecksumMismatch, err.Error())
	}

	_, err = validateContent(obj, body, manifest)
	if err != nil {
		return contentProblems(obj, err)
	}

	return nil
}

// contentProblems turns a validation error into problems, with the stable code of each field error.
func contentProblems(obj *controlplane.ManifestObject, err error) []Problem {
	verr, ok := errors.AsType[*controlplane.ValidationError](err)
	if !ok {
		return []Problem{{Object: obj.Name, Key: obj.Key, Code: ProblemInvalidContent, Detail: err.Error()}}
	}

	problems := make([]Problem, 0, len(verr.Problems))
	for i := range verr.Problems {
		problems = append(problems, Problem{
			Object: obj.Name, Key: obj.Key, Code: string(verr.Problems[i].Code), Detail: verr.Problems[i].Error(),
		})
	}

	return problems
}
