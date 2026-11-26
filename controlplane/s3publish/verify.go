package s3publish

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
		rep.Requests = counted.requests()
	}

	return rep, err
}

// owned is a decoded flag-set document and the object it came from.
type owned struct {
	doc  *controlplane.FlagSetDocument
	name string
	key  string
}

func (p *Publisher) check(ctx context.Context) (*Report, error) {
	body, etag, err := p.read(ctx, p.cfg.ManifestKey, "", p.cfg.MaxManifestBytes)

	switch {
	case isNotFound(err):
		return nil, fmt.Errorf("%w: %s", ErrNoManifest, p.cfg.ManifestKey)
	case errors.Is(err, errTooLarge):
		return &Report{
			Channel:      p.cfg.Channel,
			ManifestKey:  p.cfg.ManifestKey,
			ManifestETag: etag,
			Problems: []Problem{
				p.manifestProblem(fmt.Sprintf("the manifest is over the limit of %d bytes", p.cfg.MaxManifestBytes)),
			},
		}, nil
	case err != nil:
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

	var docs []owned

	for i := range manifest.Objects {
		obj := &manifest.Objects[i]

		if obj.Connection != p.cfg.Connection {
			rep.Skipped = append(rep.Skipped, obj.Name)

			continue
		}

		rep.Checked++

		problems, doc := p.verifyObject(ctx, obj)
		rep.Problems = append(rep.Problems, problems...)

		if doc != nil {
			docs = append(docs, owned{doc: doc, name: obj.Name, key: obj.Key})
		}
	}

	rep.Problems = append(rep.Problems, crossProblems(docs, manifest)...)

	return rep, nil
}

func (p *Publisher) manifestProblem(detail string) Problem {
	return Problem{Object: "manifest", Key: p.cfg.ManifestKey, Code: ProblemManifestInvalid, Detail: detail}
}

// crossProblems validates the flag-set documents together: duplicate flag-set names and API keys across
// documents, and sources that do not resolve. A problem is attributed to its document by position.
func crossProblems(docs []owned, manifest *controlplane.Manifest) []Problem {
	list := make([]*controlplane.FlagSetDocument, len(docs))
	for i := range docs {
		list[i] = docs[i].doc
	}

	err := controlplane.ValidateFlagSetDocuments(list, manifest)
	if err == nil {
		return nil
	}

	verr, ok := errors.AsType[*controlplane.ValidationError](err)
	if !ok {
		return []Problem{{Object: "flag-set documents", Code: ProblemInvalidContent, Detail: err.Error()}}
	}

	problems := make([]Problem, 0, len(verr.Problems))

	for i := range verr.Problems {
		fieldErr := &verr.Problems[i]
		owner := owned{name: "flag-set documents"}

		if idx, found := documentIndex(fieldErr.Path); found && idx < len(docs) {
			owner = docs[idx]
		}

		problems = append(problems, Problem{
			Object: owner.name, Key: owner.key, Code: string(fieldErr.Code), Detail: fieldErr.Error(),
		})
	}

	return problems
}

// documentIndex reads N from a path that starts with "documents[N].".
func documentIndex(path string) (int, bool) {
	rest, ok := strings.CutPrefix(path, "documents[")
	if !ok {
		return 0, false
	}

	number, _, ok := strings.Cut(rest, "]")
	if !ok {
		return 0, false
	}

	idx, err := strconv.Atoi(number)

	return idx, err == nil
}

// verifyObject checks one object and returns its problems and, for a readable flag-set document, the document.
func (p *Publisher) verifyObject(
	ctx context.Context, obj *controlplane.ManifestObject,
) ([]Problem, *controlplane.FlagSetDocument) {
	problem := func(code, detail string) ([]Problem, *controlplane.FlagSetDocument) {
		return []Problem{{Object: obj.Name, Key: obj.Key, Code: code, Detail: detail}}, nil
	}

	body, _, err := p.read(ctx, obj.Key, obj.VersionID, max(obj.Size, 0))

	var opErr *OperationError

	switch {
	case isNotFound(err), errors.Is(err, errMissingVersion):
		return problem(ProblemMissing, "no object under the listed key or version")
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

	_, doc, err := validateContent(obj, body)
	if err != nil {
		return contentProblems(obj, err), nil
	}

	if p.cfg.Check != nil {
		err = p.cfg.Check(&Object{Name: obj.Name, Kind: obj.Kind, Format: obj.Format, Source: obj.Key, Body: body})
		if err != nil {
			return problem(ProblemInvalidContent, err.Error())
		}
	}

	return nil, doc
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
