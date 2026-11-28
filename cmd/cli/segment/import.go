package segment

import (
	"context"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// ImportCmd replaces the members of a segment.
type ImportCmd struct {
	Common `embed:""`

	From         string        `help:"${helpSegFrom}"  name:"from"              required:"true"`
	Format       string        `default:"lines"        enum:"lines,csv"         help:"${helpSegFormat}" name:"format"`
	Column       int           `default:"1"            help:"${helpSegColumn}"  name:"column"`
	BatchSize    int           `default:"1000"         help:"${helpSegBatch}"   name:"batch-size"`
	MaxLineBytes int           `default:"65536"        help:"${helpSegMaxLine}" name:"max-line-bytes"`
	TempTTL      time.Duration `default:"1h"           help:"${helpSegTTL}"     name:"temp-ttl"`
	AllowEmpty   bool          `help:"${helpSegEmpty}" name:"allow-empty"`
	DryRun       bool          `help:"${helpSegDry}"   name:"dry-run"`
}

// Run imports the ids.
func (cmd *ImportCmd) Run(ctx context.Context, parent *Cmd, streams *helper.Streams, deps *Deps) error {
	src, closeSrc, err := input(cmd.From, nil, deps, segmentadmin.ReaderOptions{
		Format: segmentadmin.Format(cmd.Format), Column: cmd.Column, MaxLineBytes: cmd.MaxLineBytes,
	})
	if err != nil {
		return err
	}
	defer closeSrc()

	sess, err := open(ctx, &cmd.Common, parent.Debug, streams, deps)
	if err != nil {
		return err
	}
	defer sess.store.Close()

	rep, err := segmentadmin.Import(ctx, sess.store, sess.target, src, segmentadmin.ImportOptions{
		BatchSize: cmd.BatchSize, TempTTL: cmd.TempTTL, AllowEmpty: cmd.AllowEmpty, DryRun: cmd.DryRun,
	})
	if rep != nil {
		sess.report(ctx, cmd.Segment, "import", rep)
	}

	return wrapErr(err)
}
