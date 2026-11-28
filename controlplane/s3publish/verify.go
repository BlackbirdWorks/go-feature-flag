package s3publish

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/blackbirdworks/semaphore/controlplane"
)

// Codes of a Problem.
const (
	// ProblemManifestInvalid: the manifest cannot be decoded or fails validation.
	ProblemManifestInvalid = "manifest-invalid"
	// ProblemInvalidEntry: the entry cannot be requested (a size below 1 or over the limit, a key that is not an
	// absolute bucket key). The reader fails such an entry alone; the other entries are still checked.
	ProblemInvalidEntry = "invalid-entry"
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
	// Detail says more. The text this package writes never contains the content of a document, so no API key. The
	// text of an error returned by a Config.Check hook is the hook's own and is reported as given, except that it is
	// made single-line (control characters and newlines become spaces) and, when longer, cut so that the result, with its
	// closing "…", is at most MaxHookDetail characters (characters are runes; a rune is never cut).
	Detail string
}

// Report is the outcome of Verify.
type Report struct {
	Channel      string
	ManifestKey  string
	ManifestETag string
	// Problems lists what is wrong, in manifest order (the order of the entries of the manifest, which is by name),
	// cross-document problems included, each at the position of the entry it belongs to. Problems that belong to no
	// entry (a channel that is not the expected one) come last.
	Problems []Problem
	// Skipped lists the objects of other connections: this client reads one bucket only.
	Skipped []string
	// Requests counts the S3 calls this Verify made.
	Requests   Requests
	Generation uint64
	// Attempted counts the objects for which a read was attempted (an entry that cannot be requested is not).
	Attempted int
	// Checked counts the objects whose bytes were read and compared with the listed size and checksum: Attempted
	// without the objects that are missing or could not be read.
	Checked int
}

// MaxHookDetail is the longest text, in characters (runes), of the error of a Config.Check hook that Verify puts in
// a Problem.Detail. A text that was cut is at most this long including its closing "…".
const MaxHookDetail = 200

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
	pos  int
}

// positioned is a problem and the position, in the entries of the manifest, of the entry it belongs to.
type positioned struct {
	problem Problem
	pos     int
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

	var (
		docs  []owned
		found []positioned
	)

	for i := range manifest.Objects {
		obj := &manifest.Objects[i]

		if obj.Connection != p.cfg.Connection {
			rep.Skipped = append(rep.Skipped, obj.Name)

			continue
		}

		reason := p.entryProblem(obj)
		if reason != "" {
			found = append(found, positioned{pos: i, problem: Problem{
				Object: obj.Name, Key: obj.Key, Code: ProblemInvalidEntry, Detail: reason,
			}})

			continue
		}

		rep.Attempted++

		problems, doc, compared := p.verifyObject(ctx, obj)
		if compared {
			rep.Checked++
		}

		for _, problem := range problems {
			found = append(found, positioned{pos: i, problem: problem})
		}

		if doc != nil {
			docs = append(docs, owned{doc: doc, name: obj.Name, key: obj.Key, pos: i})
		}
	}

	found = append(found, crossProblems(docs, manifest, len(manifest.Objects))...)

	found = append(found, p.checkAll(len(manifest.Objects))...)

	if manifest.Channel != p.cfg.Channel {
		found = append(found, positioned{pos: len(manifest.Objects), problem: p.manifestProblem(
			fmt.Sprintf("channel %q, want %q", manifest.Channel, p.cfg.Channel))})
	}

	slices.SortStableFunc(found, func(a, b positioned) int { return cmp.Compare(a.pos, b.pos) })

	for _, item := range found {
		rep.Problems = append(rep.Problems, item.problem)
	}

	return rep, nil
}

func (p *Publisher) manifestProblem(detail string) Problem {
	return Problem{Object: "manifest", Key: p.cfg.ManifestKey, Code: ProblemManifestInvalid, Detail: detail}
}

// crossProblems validates the flag-set documents together: duplicate flag-set names and API keys across
// documents, and sources that do not resolve. A problem is attributed to its document by position.
func crossProblems(docs []owned, manifest *controlplane.Manifest, noEntry int) []positioned {
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
		return []positioned{{pos: noEntry, problem: Problem{
			Object: "flag-set documents", Code: ProblemInvalidContent, Detail: err.Error(),
		}}}
	}

	problems := make([]positioned, 0, len(verr.Problems))

	for i := range verr.Problems {
		fieldErr := &verr.Problems[i]
		owner := owned{name: "flag-set documents", pos: noEntry}

		if idx, found := documentIndex(fieldErr.Path); found && idx < len(docs) {
			owner = docs[idx]
		}

		problems = append(problems, positioned{pos: owner.pos, problem: Problem{
			Object: owner.name, Key: owner.key, Code: string(fieldErr.Code), Detail: fieldErr.Error(),
		}})
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

// entryProblem says why the reader fails an entry alone, or "": a size of 0, a size over MaxObjectBytes, a key with
// a leading slash. Everything that controlplane.ValidateManifest rejects (a negative size, an empty key, a bad
// checksum, a bad name, ...) never gets here: it makes the whole manifest invalid, as for the reader.
func (p *Publisher) entryProblem(obj *controlplane.ManifestObject) string {
	switch {
	case strings.HasPrefix(obj.Key, "/"):
		return "the key is not an absolute bucket key"
	case obj.Size < 1:
		return fmt.Sprintf("size %d, an object must not be empty", obj.Size)
	case obj.Size > p.cfg.MaxObjectBytes:
		return fmt.Sprintf("size %d bytes is over the limit of %d bytes", obj.Size, p.cfg.MaxObjectBytes)
	}

	return ""
}

// retain keeps a body for Config.CheckAll once for each checksum: a second object of the same content shares the body
// of the first, so the memory held is the sum over the distinct checksums, the sum that the limits of the manifest
// count and bound before any object is read.
func (p *Publisher) retain(sum string, body []byte) []byte {
	if held, ok := p.bodies[sum]; ok {
		return held
	}

	if p.bodies == nil {
		p.bodies = map[string][]byte{}
	}

	p.bodies[sum] = body

	return body
}

// MaxCheckAllProblems is the most problems that Verify reports for the errors of Config.CheckAll; the rest is
// counted in one more problem ("and N more").
const MaxCheckAllProblems = 20

// checkAll runs Config.CheckAll on the objects that were read, and returns one problem for each error that it
// reports, placed at pos. An error that joins several (errors.Join, or an error with an Unwrap() []error) is one
// problem for each of them, in order; after MaxCheckAllProblems of them the count of the others is one last problem.
func (p *Publisher) checkAll(pos int) []positioned {
	if p.cfg.CheckAll == nil {
		return nil
	}

	err := p.cfg.CheckAll(p.verified)
	if err == nil {
		return nil
	}

	all := flatten(err)
	shown := all[:min(len(all), MaxCheckAllProblems)]
	out := make([]positioned, 0, len(shown)+1)

	for _, each := range shown {
		out = append(out, positioned{pos: pos, problem: Problem{
			Object: "objects", Code: ProblemInvalidContent, Detail: hookText(each),
		}})
	}

	if more := len(all) - len(shown); more > 0 {
		out = append(out, positioned{pos: pos, problem: Problem{
			Object: "objects", Code: ProblemInvalidContent, Detail: fmt.Sprintf("and %d more", more),
		}})
	}

	return out
}

// flatten lists the leaves of an error that joins others.
func flatten(err error) []error {
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}

	var out []error

	for _, each := range multi.Unwrap() {
		out = append(out, flatten(each)...)
	}

	return out
}

// verifyObject checks one object and returns its problems and, for a readable flag-set document, the document.
func (p *Publisher) verifyObject(
	ctx context.Context, obj *controlplane.ManifestObject,
) ([]Problem, *controlplane.FlagSetDocument, bool) {
	problem := func(code, detail string, compared bool) ([]Problem, *controlplane.FlagSetDocument, bool) {
		return []Problem{{Object: obj.Name, Key: obj.Key, Code: code, Detail: detail}}, nil, compared
	}

	body, _, err := p.read(ctx, obj.Key, obj.VersionID, max(obj.Size, 0))

	var opErr *OperationError

	switch {
	case isNotFound(err), errors.Is(err, errMissingVersion):
		return problem(ProblemMissing, "no object under the listed key or version", false)
	case errors.As(err, &opErr) && errors.Is(opErr.Err, errTooLarge):
		return problem(ProblemSizeMismatch, fmt.Sprintf("larger than the listed %d bytes", obj.Size), true)
	case err != nil:
		return problem(ProblemUnreadable, err.Error(), false)
	}

	if int64(len(body)) != obj.Size {
		return problem(ProblemSizeMismatch, fmt.Sprintf("%d bytes, listed %d", len(body), obj.Size), true)
	}

	err = controlplane.VerifyChecksum(body, obj.Checksum)
	if err != nil {
		return problem(ProblemChecksumMismatch, err.Error(), true)
	}

	_, doc, err := validateContent(obj, body)
	if err != nil {
		return contentProblems(obj, err), nil, true
	}

	if p.cfg.Check != nil {
		err = p.cfg.Check(&Object{Name: obj.Name, Kind: obj.Kind, Format: obj.Format, Source: obj.Key, Body: body})
		if err != nil {
			// The document was decoded: it still takes part in the checks across documents.
			problems, _, _ := problem(ProblemInvalidContent, hookText(err), true)

			return problems, doc, true
		}
	}

	if p.cfg.CheckAll != nil {
		held := p.retain(obj.Checksum.Value, body)

		p.verified = append(
			p.verified,
			Object{Name: obj.Name, Kind: obj.Kind, Format: obj.Format, Source: obj.Key, Body: held},
		)
	}

	return nil, doc, true
}

// hookText makes the text of the error of a Config.Check hook fit for a Problem.Detail: one line (a control character
// or a newline becomes a space) of at most MaxHookDetail runes, the ellipsis of a cut text included. It never cuts
// inside a rune.
func hookText(err error) string {
	text := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}

		return r
	}, err.Error())

	text = strings.Join(strings.Fields(text), " ")

	runes := []rune(text)
	if len(runes) > MaxHookDetail {
		return string(runes[:MaxHookDetail-1]) + "…"
	}

	return text
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
