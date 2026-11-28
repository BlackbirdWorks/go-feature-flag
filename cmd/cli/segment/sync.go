package segment

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// SyncCmd keeps the segment in step with an s3 object or prefix.
type SyncCmd struct {
	Common `embed:""`

	SourceConnection string        `help:"${helpSegSrcConn}" name:"source-connection"`
	From             string        `help:"${helpSegFrom}"    name:"from"               required:"true"`
	Format           string        `default:"lines"          enum:"lines,csv"          help:"${helpSegFormat}" name:"format"`
	MaxObjects       int           `default:"1000"           help:"${helpSegMaxObj}"   name:"max-objects"`
	Column           int           `default:"1"              help:"${helpSegColumn}"   name:"column"`
	BatchSize        int           `default:"1000"           help:"${helpSegBatch}"    name:"batch-size"`
	MaxLineBytes     int           `default:"65536"          help:"${helpSegMaxLine}"  name:"max-line-bytes"`
	TempTTL          time.Duration `default:"1h"             help:"${helpSegTTL}"      name:"temp-ttl"`
	Interval         time.Duration `default:"1m"             help:"${helpSegInterval}" name:"interval"`
	AllowEmpty       bool          `help:"${helpSegEmpty}"   name:"allow-empty"`
	Once             bool          `help:"${helpSegOnce}"    name:"once"`
}

// Run checks the source, and imports it when its fingerprint is not the one recorded with the live set. It runs until
// the context ends, or once with --once: then a failed round is the error of the command.
func (cmd *SyncCmd) Run(ctx context.Context, parent *Cmd, streams *helper.Streams, deps *Deps) error {
	if !isObjectURL(cmd.From) {
		return fmt.Errorf("%w: --from must be s3://<bucket>/<key> or s3://<bucket>/<prefix>/: %w",
			helper.ErrUsage, segmentadmin.ErrObjectURL)
	}

	sess, err := open(ctx, &cmd.Common, parent.Debug, streams, deps)
	if err != nil {
		return wrapErr(err)
	}
	defer sess.store.Close()

	source, err := objectSource(ctx, sess, cmd.From, cmd.SourceConnection, cmd.MaxObjects, deps)
	if err != nil {
		return wrapErr(err)
	}

	opts := &segmentadmin.SyncOptions{
		Log:      slog.New(slog.NewTextHandler(streams.Stderr, nil)),
		Interval: cmd.Interval,
		Reader: segmentadmin.ReaderOptions{
			Format: segmentadmin.Format(cmd.Format), Column: cmd.Column, MaxLineBytes: cmd.MaxLineBytes,
		},
		Import: segmentadmin.ImportOptions{BatchSize: cmd.BatchSize, TempTTL: cmd.TempTTL, AllowEmpty: cmd.AllowEmpty},
	}

	if !cmd.Once {
		segmentadmin.Sync(ctx, sess.store, sess.target, source, opts)

		return nil
	}

	return wrapErr(segmentadmin.SyncRound(ctx, sess.store, sess.target, source, opts).Err)
}
