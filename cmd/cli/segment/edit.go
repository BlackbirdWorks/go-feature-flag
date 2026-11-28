package segment

import (
	"context"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// AddCmd adds ids.
type AddCmd struct {
	Common `embed:""`

	From string   `help:"${helpSegFrom}" name:"from"`
	IDs  []string `arg:""                help:"${helpSegIDs}" name:"id" optional:""`
}

// RemoveCmd removes ids.
type RemoveCmd struct {
	Common `embed:""`

	From string   `help:"${helpSegFrom}" name:"from"`
	IDs  []string `arg:""                help:"${helpSegIDs}" name:"id" optional:""`
}

// Run adds the ids.
func (cmd *AddCmd) Run(ctx context.Context, parent *Cmd, streams *helper.Streams, deps *Deps) error {
	return edit(ctx, &cmd.Common, parent.Debug, streams, deps, cmd.From, cmd.IDs, false)
}

// Run removes the ids.
func (cmd *RemoveCmd) Run(ctx context.Context, parent *Cmd, streams *helper.Streams, deps *Deps) error {
	return edit(ctx, &cmd.Common, parent.Debug, streams, deps, cmd.From, cmd.IDs, true)
}

func edit(
	ctx context.Context, com *Common, debug bool, streams *helper.Streams, deps *Deps, from string, ids []string,
	remove bool,
) error {
	src, closeSrc, err := input(ctx, from, ids, deps, segmentadmin.ReaderOptions{})
	if err != nil {
		return wrapErr(err)
	}
	defer closeSrc()

	sess, err := open(ctx, com, debug, streams, deps)
	if err != nil {
		return wrapErr(err)
	}
	defer sess.store.Close()

	rep, err := segmentadmin.Edit(ctx, sess.store, sess.target, src, remove, 0)

	verb := "add"
	if remove {
		verb = "remove"
	}

	if rep != nil {
		reportErr := sess.report(ctx, com.Segment, verb, rep)
		if err == nil {
			err = reportErr
		}
	}

	return wrapErr(err)
}
