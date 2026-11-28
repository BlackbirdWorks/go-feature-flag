package segment

import (
	"context"
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// ImportCmd replaces the members of a segment.
type ImportCmd struct {
	Common `embed:""`

	SourceConnection string        `help:"${helpSegSrc}"   name:"source-connection"`
	From             string        `help:"${helpSegFrom}"  name:"from"              required:"true"`
	Format           string        `default:"lines"        enum:"lines,csv"         help:"${helpSegFormat}" name:"format"`
	MaxObjects       int           `default:"1000"         help:"${helpSegMaxObj}"  name:"max-objects"`
	Column           int           `default:"1"            help:"${helpSegColumn}"  name:"column"`
	BatchSize        int           `default:"1000"         help:"${helpSegBatch}"   name:"batch-size"`
	MaxLineBytes     int           `default:"65536"        help:"${helpSegMaxLine}" name:"max-line-bytes"`
	TempTTL          time.Duration `default:"1h"           help:"${helpSegTTL}"     name:"temp-ttl"`
	AllowEmpty       bool          `help:"${helpSegEmpty}" name:"allow-empty"`
	DryRun           bool          `help:"${helpSegDry}"   name:"dry-run"`
}

// Run imports the ids. A file or standard input is opened before the store is, so that a bad input fails first; an
// s3 source needs the bootstrap file, which the session has read.
func (cmd *ImportCmd) Run(ctx context.Context, parent *Cmd, streams *helper.Streams, deps *Deps) error {
	opts := segmentadmin.ReaderOptions{
		Format: segmentadmin.Format(cmd.Format), Column: cmd.Column, MaxLineBytes: cmd.MaxLineBytes,
	}

	if isObjectURL(cmd.From) {
		sess, err := open(ctx, &cmd.Common, parent.Debug, streams, deps)
		if err != nil {
			return wrapErr(err)
		}
		defer sess.store.Close()

		stream, err := cmd.openObjects(ctx, sess, deps, opts)
		if err != nil {
			return wrapErr(err)
		}
		defer stream.Close()

		return cmd.swapIn(ctx, sess, stream, stream.Fingerprint())
	}

	src, closeSrc, err := input(ctx, cmd.From, nil, deps, opts)
	if err != nil {
		return wrapErr(err)
	}
	defer closeSrc()

	sess, err := open(ctx, &cmd.Common, parent.Debug, streams, deps)
	if err != nil {
		return wrapErr(err)
	}
	defer sess.store.Close()

	return cmd.swapIn(ctx, sess, src, "")
}

// swapIn imports src. The fingerprint of a source that has one is recorded with the swap.
func (cmd *ImportCmd) swapIn(ctx context.Context, sess *session, src segmentadmin.Source, fingerprint string) error {
	rep, err := segmentadmin.Import(ctx, sess.store, sess.target, src, segmentadmin.ImportOptions{
		BatchSize: cmd.BatchSize, TempTTL: cmd.TempTTL, AllowEmpty: cmd.AllowEmpty, DryRun: cmd.DryRun,
		Fingerprint: fingerprint,
	})
	if rep != nil {
		reportErr := sess.report(ctx, cmd.Segment, "import", rep)
		if err == nil {
			err = reportErr
		}
	}

	return wrapErr(err)
}

func (cmd *ImportCmd) openObjects(
	ctx context.Context, sess *session, deps *Deps, opts segmentadmin.ReaderOptions,
) (*segmentadmin.ObjectStream, error) {
	source, err := objectSource(ctx, sess, cmd.From, cmd.SourceConnection, cmd.MaxObjects, deps)
	if err != nil {
		return nil, err
	}

	stream, err := source.Open(ctx, "", opts)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}

	return stream, nil
}
