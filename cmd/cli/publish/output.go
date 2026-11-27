package publish

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
)

// The values of --output.
const (
	outputText = "text"
	outputJSON = "json"
)

// jsonRequests is the request count of a result.
type jsonRequests struct {
	Get   int `json:"get"`
	Head  int `json:"head"`
	Put   int `json:"put"`
	Total int `json:"total"`
}

// jsonObject is one manifest entry of the output.
type jsonObject struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Key    string `json:"key"`
	Size   int64  `json:"size"`
}

// jsonResult is the JSON output of publish.
type jsonResult struct { //nolint:govet // field order is the order of the JSON output
	Channel            string       `json:"channel"`
	ManifestKey        string       `json:"manifestKey"`
	ManifestETag       string       `json:"manifestETag"`
	Generation         uint64       `json:"generation"`
	PreviousGeneration uint64       `json:"previousGeneration"`
	Changed            bool         `json:"changed"`
	Written            bool         `json:"written"`
	DryRun             bool         `json:"dryRun"`
	Uploaded           int          `json:"uploaded"`
	Present            int          `json:"present"`
	UploadedBytes      int64        `json:"uploadedBytes"`
	Requests           jsonRequests `json:"requests"`
	Objects            []jsonObject `json:"objects"`
	Warnings           []string     `json:"warnings"`
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	err := enc.Encode(v)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

func requestsOf(r s3publish.Requests) jsonRequests {
	return jsonRequests{Get: r.Get, Head: r.Head, Put: r.Put, Total: r.Total()}
}

// printResult writes the outcome of Publish.
func printResult(w io.Writer, format string, res *s3publish.Result) error {
	if format == outputJSON {
		out := jsonResult{
			Channel:            res.Channel,
			ManifestKey:        res.ManifestKey,
			ManifestETag:       res.ManifestETag,
			Generation:         res.Generation,
			PreviousGeneration: res.Previous,
			Changed:            res.Changed,
			Written:            res.Written,
			DryRun:             res.DryRun,
			Uploaded:           res.Uploaded,
			Present:            res.Present,
			UploadedBytes:      res.UploadedBytes,
			Requests:           requestsOf(res.Requests),
			Objects:            make([]jsonObject, 0, len(res.Objects)),
			Warnings:           append([]string{}, res.Warnings...),
		}
		for _, o := range res.Objects {
			out.Objects = append(
				out.Objects,
				jsonObject{Name: o.Name, Status: string(o.Status), Key: o.Key, Size: o.Size},
			)
		}

		return writeJSON(w, out)
	}

	return printResultText(w, res)
}

// lineWriter writes lines and keeps the first error.
type lineWriter struct {
	out io.Writer
	err error
}

func (l *lineWriter) printf(format string, args ...any) {
	if l.err != nil {
		return
	}

	_, err := fmt.Fprintf(l.out, format+"\n", args...)
	if err != nil {
		l.err = fmt.Errorf("write output: %w", err)
	}
}

func printResultText(w io.Writer, res *s3publish.Result) error {
	out := &lineWriter{out: w}
	line := out.printf

	switch {
	case !res.Changed && res.Uploaded > 0:
		again := "uploaded again"
		if res.DryRun {
			again = "would be uploaded again"
		}

		line("no change: %s already lists these documents at generation %d; %d missing object(s) %s",
			res.ManifestKey, res.Generation, res.Uploaded, again)
	case !res.Changed:
		line(
			"no change: %s already lists these documents at generation %d; nothing written",
			res.ManifestKey,
			res.Generation,
		)
	case res.DryRun:
		line("dry run: would write generation %d (the bucket holds %d) to %s, channel %s; nothing written",
			res.Generation, res.Previous, res.ManifestKey, res.Channel)
	default:
		line(
			"published generation %d (was %d) to %s, channel %s",
			res.Generation,
			res.Previous,
			res.ManifestKey,
			res.Channel,
		)
	}

	if res.Changed {
		verb := "uploaded"
		if res.DryRun {
			verb = "to upload"
		}

		line(
			"objects: %d %s, %d already present, %d bytes %s",
			res.Uploaded,
			verb,
			res.Present,
			res.UploadedBytes,
			verb,
		)
	}

	line("requests: %d get, %d head, %d put", res.Requests.Get, res.Requests.Head, res.Requests.Put)

	if res.ManifestETag != "" {
		line("manifest etag: %s", res.ManifestETag)
	}

	for _, o := range res.Objects {
		line("  %-12s  %s  %s (%d bytes)", o.Status, o.Name, o.Key, o.Size)
	}

	for _, warning := range res.Warnings {
		line("warning: %s", warning)
	}

	return out.err
}

// jsonProblem is one problem of a verification.
type jsonProblem struct {
	Object string `json:"object"`
	Code   string `json:"code"`
	Key    string `json:"key"`
	Detail string `json:"detail"`
}

// jsonReport is the JSON output of publish verify.
type jsonReport struct { //nolint:govet // field order is the order of the JSON output
	Channel      string        `json:"channel"`
	ManifestKey  string        `json:"manifestKey"`
	ManifestETag string        `json:"manifestETag"`
	Generation   uint64        `json:"generation"`
	OK           bool          `json:"ok"`
	Checked      int           `json:"checked"`
	Skipped      []string      `json:"skipped"`
	Requests     jsonRequests  `json:"requests"`
	Problems     []jsonProblem `json:"problems"`
}

// printReport writes the outcome of Verify.
func printReport(w io.Writer, format string, rep *s3publish.Report) error {
	if format == outputJSON {
		out := jsonReport{
			Channel:      rep.Channel,
			ManifestKey:  rep.ManifestKey,
			ManifestETag: rep.ManifestETag,
			Generation:   rep.Generation,
			OK:           rep.OK(),
			Checked:      rep.Checked,
			Skipped:      append([]string{}, rep.Skipped...),
			Requests:     requestsOf(rep.Requests),
			Problems:     make([]jsonProblem, 0, len(rep.Problems)),
		}
		for _, p := range rep.Problems {
			out.Problems = append(
				out.Problems,
				jsonProblem{Object: p.Object, Code: p.Code, Key: p.Key, Detail: p.Detail},
			)
		}

		return writeJSON(w, out)
	}

	out := &lineWriter{out: w}
	line := out.printf

	line(
		"manifest %s, channel %s, generation %d, etag %s",
		rep.ManifestKey,
		rep.Channel,
		rep.Generation,
		rep.ManifestETag,
	)
	line("checked %d objects, skipped %d (other connections)", rep.Checked, len(rep.Skipped))
	line("requests: %d get", rep.Requests.Get)

	for _, p := range rep.Problems {
		line("problem: %s %s %s: %s", p.Object, p.Code, p.Key, p.Detail)
	}

	if len(rep.Skipped) > 0 {
		line(
			"note: %d object(s) of other connections were not checked: %s",
			len(rep.Skipped),
			strings.Join(rep.Skipped, ", "),
		)
	}

	if rep.OK() {
		line("ok: every checked object matches its size and checksum")
	}

	return out.err
}
