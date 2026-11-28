package segment

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// StatCmd shows a segment.
type StatCmd struct {
	Common `embed:""`

	ScanLimit int `default:"1000000" help:"${helpSegScan}" name:"scan-limit"`
}

// Run shows the segment.
func (cmd *StatCmd) Run(ctx context.Context, parent *Cmd, streams *helper.Streams, deps *Deps) error {
	sess, err := open(ctx, &cmd.Common, parent.Debug, streams, deps)
	if err != nil {
		return err
	}
	defer sess.store.Close()

	stats, err := segmentadmin.Stat(ctx, sess.store, sess.target, cmd.ScanLimit)
	if err != nil {
		return wrapErr(err)
	}

	count := strconv.FormatInt(stats.Count, 10)
	if stats.AtLeast {
		count = "at least " + count
	}

	lines := []string{"segment: " + cmd.Segment, fmt.Sprintf("exists: %t", stats.Exists), "members: " + count}
	if sess.target.VersionKey != "" {
		lines = append(lines, "version: "+orNone(stats.Version, stats.HasVersion))
	}

	if stats.TTL > 0 {
		lines = append(lines, "ttl: "+stats.TTL.String())
	}

	_, err = fmt.Fprintln(sess.out, strings.Join(lines, "\n"))
	if err != nil {
		return fmt.Errorf("write the result: %w", err)
	}

	return nil
}

func orNone(val string, ok bool) string {
	if !ok {
		return "none"
	}

	return val
}
