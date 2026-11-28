package segment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/modules/core/segment/segmentadmin"
)

// ErrNoInput is the failure of add or remove without an id and without --from.
var ErrNoInput = errors.New("give at least one id, or --from <file|->")

// HelpVars returns the kong interpolation variables used by [Cmd] help tags.
func HelpVars() kong.Vars {
	return kong.Vars{
		"helpSegment": "Load and edit the members of a dynamic segment in its Valkey or Redis store",
		"helpSegImport": "Replace the members of a segment from a file, standard input or S3, with an atomic swap " +
			"(a temporary key, then RENAME)",
		"helpSegAdd":    "Add ids to the segment and bump its version key",
		"helpSegRemove": "Remove ids from the segment and bump its version key",
		"helpSegBump":   "Bump the version key of the segment only, so that the relays read it again",
		"helpSegStat":   "Show whether the segment exists, how many members it has, its version and its ttl",
		"helpSegFrom": "File of ids, - for standard input, or s3://bucket/key " +
			"(s3://bucket/prefix/ for every object under it)",
		"helpSegConfig": "Bootstrap file with the connections (or SEMAPHORE_CONFIG)",
		"helpSegDoc": "Segments document (YAML or JSON) that defines the segment; without it the segments objects " +
			"of the control plane are read",
		"helpSegName":    "Name of the segment in the document",
		"helpSegDebug":   "Log each step to stderr (counts only, never an id)",
		"helpSegFormat":  "Input format: lines or csv",
		"helpSegColumn":  "CSV column of the id, from 1",
		"helpSegBatch":   "Ids of one SADD",
		"helpSegMaxLine": "Longest line; a longer one is rejected",
		"helpSegTTL":     "Life of the temporary key, refreshed by each batch",
		"helpSegEmpty":   "Accept an input with no id: the segment is emptied",
		"helpSegDry":     "Read and validate the input, write nothing",
		"helpSegIDs":     "Ids to add or remove",
		"helpSegScan":    "Most keys that a keys layout segment is counted to",
		"helpSegSrc": "Name of the s3 connection of the bootstrap file that holds the source (needed when it has " +
			"more than one)",
		"helpSegMaxObj":   "Most objects of an s3 prefix; more is an error, never a partial read",
		"helpSegSegment":  "Segment to keep in step with the source",
		"helpSegInterval": "Pause between two checks of the source",
		"helpSegOnce":     "Check once and exit (0 when the source is unchanged or imported)",
		"helpSegSync":     "Keep the segment in step with an s3 object or prefix: import it when its fingerprint changes",
	}
}

// Cmd is the segment command group.
type Cmd struct {
	Add    AddCmd    `cmd:""                 help:"${helpSegAdd}"`
	Remove RemoveCmd `cmd:""                 help:"${helpSegRemove}"`
	Stat   StatCmd   `cmd:""                 help:"${helpSegStat}"`
	Bump   BumpCmd   `cmd:""                 help:"${helpSegBump}"`
	Import ImportCmd `cmd:""                 help:"${helpSegImport}"`
	Sync   SyncCmd   `cmd:""                 help:"${helpSegSync}"`
	Debug  bool      `help:"${helpSegDebug}" name:"debug"`
}

// Common are the flags that name the segment and the store.
type Common struct {
	Config   string `help:"${helpSegConfig}" name:"config"   short:"c"`
	Segments string `help:"${helpSegDoc}"    name:"segments"`
	Segment  string `help:"${helpSegName}"   name:"segment"  required:"true"`
}

// session is a resolved segment and its connected store.
type session struct {
	store  segmentadmin.Store
	res    *resolved
	target *segmentadmin.Target
	log    *slog.Logger
	out    io.Writer
}

func open(ctx context.Context, com *Common, debug bool, streams *helper.Streams, deps *Deps) (*session, error) {
	res, err := resolve(ctx, com, deps)
	if err != nil {
		return nil, err
	}

	logger := slog.New(slog.DiscardHandler)
	if debug {
		logger = slog.New(slog.NewTextHandler(streams.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}

	store, err := deps.Connect(ctx, res.conn, res.secrets)
	if err != nil {
		return nil, fmt.Errorf("connection %q: %w", res.connName, err)
	}

	err = store.Ping(ctx)
	if err != nil {
		store.Close()

		return nil, fmt.Errorf("connection %q: %w", res.connName, err)
	}

	logger.LogAttrs(ctx, slog.LevelDebug, "segment resolved", slog.String("segment", com.Segment),
		slog.String("connection", res.connName), slog.String("layout", string(res.target.Layout)))

	return &session{store: store, res: res, target: res.target, log: logger, out: streams.Stdout}, nil
}

// inputSource is the ids of one command: a file, standard input or the arguments.
type inputSource struct {
	segmentadmin.Source
}

// input opens the ids of --from, or the ids of the arguments. A read of standard input ends with the context.
func input(
	ctx context.Context, from string, ids []string, deps *Deps, opts segmentadmin.ReaderOptions,
) (*inputSource, func(), error) {
	closer := func() {}

	switch {
	case from == "-":
		return newReader(segmentadmin.WithContext(ctx, deps.Stdin), opts, closer)
	case from != "":
		file, err := deps.Open(from)
		if err != nil {
			return nil, closer, fmt.Errorf("open the input: %w", err)
		}

		return newReader(segmentadmin.WithContext(ctx, file), opts, func() { _ = file.Close() })
	case len(ids) > 0:
		return &inputSource{Source: segmentadmin.NewSliceSource(ids)}, closer, nil
	}

	return nil, closer, fmt.Errorf("%w: %w", helper.ErrUsage, ErrNoInput)
}

func newReader(
	src io.Reader, opts segmentadmin.ReaderOptions, closer func(),
) (*inputSource, func(), error) {
	reader, err := segmentadmin.NewReader(src, opts)
	if err != nil {
		closer()

		return nil, func() {}, fmt.Errorf("input: %w", wrapErr(err))
	}

	return &inputSource{Source: reader}, closer, nil
}

// wrapErr gives an invalid input the exit code of invalid input, and a failure of the store the one of a transport.
func wrapErr(err error) error {
	var code int

	switch {
	case err == nil:
		return nil
	case errors.Is(err, segmentadmin.ErrEmptyInput), errors.Is(err, segmentadmin.ErrKeysLayout),
		errors.Is(err, segmentadmin.ErrBadOptions), errors.Is(err, segmentadmin.ErrKeyOverlap),
		errors.Is(err, segmentadmin.ErrObjectURL), errors.Is(err, segmentadmin.ErrTooManyObjects),
		errors.Is(err, ErrSourceConnection), errors.Is(err, ErrNoControlPlane):
		code = helper.ExitInvalid
	case errors.Is(err, segmentadmin.ErrImportRunning), errors.Is(err, segmentadmin.ErrLockLost),
		errors.Is(err, segmentadmin.ErrTempGone), errors.Is(err, segmentadmin.ErrIncomplete),
		errors.Is(err, segmentadmin.ErrSourceChanged), errors.Is(err, segmentadmin.ErrObjectChanged):
		code = helper.ExitConflict
	case errors.Is(err, segmentadmin.ErrSwapUnknown):
		code = helper.ExitUnknown
	case errors.Is(err, segmentadmin.ErrStore):
		code = helper.ExitTransport
	default:
		return err
	}

	return fmt.Errorf("%w", helper.WithExitCode(err, code))
}

// report prints the counts of an operation.
func (s *session) report(ctx context.Context, name, verb string, rep *segmentadmin.Report) error {
	var lines strings.Builder

	fmt.Fprintf(&lines, "segment: %s\noperation: %s\nread: %d\naccepted: %d\n", name, verb, rep.Read, rep.Accepted)

	switch {
	case rep.DryRun:
		lines.WriteString("dry run: nothing written\n")
	case verb == "remove":
		fmt.Fprintf(&lines, "removed: %d\n", rep.Removed)
	default:
		fmt.Fprintf(&lines, "added: %d\n", rep.Added)

		if verb == "import" {
			if rep.DuplicatesUnknown {
				lines.WriteString("duplicates: unknown (a batch was sent again after a lost reply)\n")
			} else {
				fmt.Fprintf(&lines, "duplicates: %d\n", rep.Duplicates)
			}

			fmt.Fprintf(&lines, "swapped: %t\n", rep.Swapped)
		}
	}

	rej := &rep.Rejected
	for _, one := range []struct {
		reason segmentadmin.Reason
		count  int64
	}{
		{segmentadmin.ReasonEmpty, rej.Empty}, {segmentadmin.ReasonTooLong, rej.TooLong},
		{segmentadmin.ReasonInvalid, rej.Invalid}, {segmentadmin.ReasonColumn, rej.Column},
	} {
		if one.count > 0 {
			fmt.Fprintf(&lines, "rejected %s: %d\n", one.reason, one.count)
		}
	}

	if rep.Version != "" {
		fmt.Fprintf(&lines, "version: %s\n", rep.Version)
	}

	if rep.FellBackToDel {
		lines.WriteString("note: the server has no UNLINK, DEL was used\n")
	}

	if rep.FallbackLarge {
		lines.WriteString(
			"warning: DEL removed a large set and may have blocked the server; upgrade to a server with UNLINK\n",
		)
	}

	if rep.CleanupFailed {
		lines.WriteString("note: the temporary key was not removed, it expires\n")
	}

	_, err := io.WriteString(s.out, lines.String())

	s.log.LogAttrs(ctx, slog.LevelDebug, "done", slog.String("operation", verb), slog.Int64("read", rep.Read),
		slog.Int64("accepted", rep.Accepted), slog.Int64("added", rep.Added))

	if err != nil {
		return fmt.Errorf("write the result: %w", err)
	}

	return nil
}

// Run parses args against the segment command tree and runs the selected subcommand. It is for tests and embedders;
// the semaphore-cli binary mounts [Cmd] in its own tree.
func Run(ctx context.Context, args []string, streams *helper.Streams, deps *Deps) error {
	var runner struct {
		Cmd
	}

	parser, err := kong.New(&runner,
		kong.Name("segment"),
		kong.Exit(func(int) {}),
		kong.KindMapper(reflect.String, kong.MapperFunc(helper.DecodeString)),
		kong.Writers(streams.Stdout, streams.Stderr),
		HelpVars(),
	)
	if err != nil {
		return fmt.Errorf("build segment parser: %w", err)
	}

	kctx, err := parser.Parse(args)
	if err != nil {
		return fmt.Errorf("%w: %w", helper.ErrUsage, err)
	}

	kctx.BindTo(ctx, (*context.Context)(nil))

	err = kctx.Run(&runner.Cmd, streams, deps)
	if err != nil {
		return fmt.Errorf("segment: %w", err)
	}

	return nil
}
