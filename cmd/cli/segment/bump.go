package segment

import (
	"context"
	"errors"
	"fmt"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// ErrNoVersionKey is the failure of bump for a definition without a version key.
var ErrNoVersionKey = errors.New("the definition has no version.key to bump")

// BumpCmd bumps the version key of a segment.
type BumpCmd struct {
	Common `embed:""`
}

// Run bumps the version key.
func (cmd *BumpCmd) Run(ctx context.Context, parent *Cmd, streams *helper.Streams, deps *Deps) error {
	sess, err := open(ctx, &cmd.Common, parent.Debug, streams, deps)
	if err != nil {
		return wrapErr(err)
	}
	defer sess.store.Close()

	if sess.target.VersionKey == "" {
		return fmt.Errorf("%w: %w", helper.ErrUsage, ErrNoVersionKey)
	}

	rep := &segmentadmin.Report{}

	err = segmentadmin.BumpWithRetry(ctx, sess.store, sess.target, rep, nil)
	if err != nil {
		return wrapErr(err)
	}

	_, err = fmt.Fprintf(sess.out, "segment: %s\nversion: %s\n", cmd.Segment, rep.Version)
	if err != nil {
		return fmt.Errorf("write the result: %w", err)
	}

	return nil
}
