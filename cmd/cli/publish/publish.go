package publish

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/controlplane"
	"github.com/blackbirdworks/semaphore/controlplane/flagcheck"
	"github.com/blackbirdworks/semaphore/controlplane/s3publish"
	"github.com/blackbirdworks/semaphore/internal/connection"
)

// options are the flags of publish and publish verify.
type options struct {
	target     targetFlags
	output     string
	generation uint64
	dryRun     bool
	replace    bool
}

// NewPublishCmd returns the publish command with its verify subcommand, wired to the real environment.
func NewPublishCmd() *cobra.Command { return NewPublishCmdWith(DefaultDeps()) }

// NewPublishCmdWith returns the publish command with the given dependencies.
func NewPublishCmdWith(deps Deps) *cobra.Command {
	opts := &options{}

	cmd := &cobra.Command{
		Use:   "publish <dir>",
		Short: "Publish flag files and flag-set documents to S3 behind a manifest",
		Long: `Reads the documents of <dir> (flags/*.yaml|yml|json and flagsets/*.yaml|yml|json), validates them,
uploads the objects that are absent under their content-addressed keys, and writes the manifest last with a
conditional write, so a reader never sees a half-written state.

Where to publish comes from the control-plane connection of a bootstrap file (--config), or from --bucket and
--region. Exit codes: 0 success, 1 failure or bad usage, 2 invalid documents or configuration, 3 conflict (another
publisher won; nothing visible changed), 4 store call failed (safe to repeat), 5 the bucket holds a bad state.`,
		Args:          jsonArgs(opts, cobra.ExactArgs(1)),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return reportError(cmd, opts, runPublish(cmd, args[0], opts, &deps))
		},
	}

	addTargetFlags(cmd, opts)
	// A flag that does not parse is reported as JSON too, when -o json came before it (flags are read in order).
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return reportError(cmd, opts, fmt.Errorf("%w: %w", ErrUsage, err))
	})
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "plan the publication: the same reads, no write")
	cmd.Flags().
		Uint64Var(&opts.generation, "generation", 0, "generation to write (default: the one in the bucket plus one)")
	cmd.Flags().BoolVar(&opts.replace, "replace-invalid-manifest", false,
		"replace a manifest in the bucket that is invalid or over the limits (needs --generation)")

	cmd.AddCommand(newVerifyCmd(opts, &deps))

	return cmd
}

// addTargetFlags registers the flags that say where to publish, and --output, on the command and its children.
func addTargetFlags(cmd *cobra.Command, opts *options) {
	flagSet := cmd.PersistentFlags()
	flagSet.StringVarP(
		&opts.target.config,
		"config",
		"c",
		"",
		"bootstrap file whose controlPlane connection is the target",
	)
	flagSet.StringVar(&opts.target.bucket, "bucket", "", "bucket (without --config)")
	flagSet.StringVar(&opts.target.prefix, "prefix", "", "key prefix of the objects and of the default manifest key")
	flagSet.StringVar(&opts.target.region, "region", "", "AWS region (without --config)")
	flagSet.StringVar(
		&opts.target.endpoint,
		"endpoint",
		"",
		"S3 endpoint, for emulators and S3 compatible stores (without --config)",
	)
	flagSet.StringVar(&opts.target.channel, "channel", "", "channel of the manifest (without --config; default stable)")
	flagSet.StringVar(
		&opts.target.connection,
		"connection",
		"",
		"connection name written in the manifest (without --config; default control)",
	)
	flagSet.StringVar(
		&opts.target.manifestKey,
		"manifest-key",
		"",
		"manifest key (without --config; default <prefix>manifests/<channel>.yaml)",
	)
	flagSet.StringVarP(&opts.output, "output", "o", outputText, "output format: text or json")
}

func checkOutput(format string) error {
	if format != outputText && format != outputJSON {
		return fmt.Errorf("%w: --output must be text or json, not %q", ErrUsage, format)
	}

	return nil
}

func runPublish(cmd *cobra.Command, dir string, opts *options, deps *Deps) error {
	err := checkOutput(opts.output)
	if err != nil {
		return err
	}

	tgt, err := resolveTarget(&opts.target, deps)
	if err != nil {
		return withCode(err)
	}

	objects, err := LoadSource(deps.OpenDir(dir), dir, tgt.sourceLimits())
	if err != nil {
		return withCode(err)
	}

	tgt.cfg.Check = checkFlags

	pub, err := newPublisher(cmd, tgt, deps)
	if err != nil {
		return withCode(err)
	}

	created := deps.Now().UTC().Truncate(time.Second)

	res, err := pub.Publish(cmd.Context(), s3publish.Request{
		Objects: objects, Generation: opts.generation, CreatedAt: &created, DryRun: opts.dryRun,
		ReplaceInvalidManifest: opts.replace,
	})
	if err != nil {
		return withCode(err)
	}

	return printResult(cmd.OutOrStdout(), opts.output, res)
}

// jsonArgs wraps an argument validator so that its failure is a usage error, written as JSON when --output json
// was given (cobra validates the arguments before RunE runs).
func jsonArgs(opts *options, validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		err := validate(cmd, args)
		if err == nil {
			return nil
		}

		return reportError(cmd, opts, fmt.Errorf("%w: %w", ErrUsage, err))
	}
}

// checkFlags validates a flag file with the engine of the relay (the flagcheck package).
func checkFlags(obj *s3publish.Object) error {
	if obj.Kind != controlplane.KindFlags {
		return nil
	}

	err := flagcheck.Check(obj.Body, obj.Format)
	if err != nil {
		return fmt.Errorf("engine check: %w", err)
	}

	return nil
}

// reportError writes the JSON error object of a failed command when --output is json, and returns the error. The
// report of a failed verification is already the output of the command, so it gets no second document.
func reportError(cmd *cobra.Command, opts *options, err error) error {
	if err == nil || opts.output != outputJSON || errors.Is(err, errVerify) {
		return err
	}

	err = withCode(err)
	code := helper.ExitCodeOf(err)
	name := helper.JSONErrorName(code)

	if errors.Is(err, ErrUsage) {
		name = "usage"
	}

	writeErr := helper.WriteJSONError(cmd.OutOrStdout(), name, err, errorDetails(err))
	if writeErr != nil {
		return fmt.Errorf("report error: %w", writeErr)
	}

	return err
}

// errorDetails lists the individual problems behind an error: the field errors of a validation and the flags
// that the engine rejects, in the order reported.
func errorDetails(err error) []string {
	var details []string

	if verr, ok := errors.AsType[*controlplane.ValidationError](err); ok {
		for i := range verr.Problems {
			details = append(details, verr.Problems[i].Error())
		}
	}

	if ferr, ok := errors.AsType[*flagcheck.Error](err); ok {
		for i := range ferr.Problems {
			details = append(details, fmt.Sprintf("flag %s: %v", ferr.Problems[i].Flag, ferr.Problems[i].Err))
		}
	}

	return details
}

func newPublisher(cmd *cobra.Command, tgt *target, deps *Deps) (*s3publish.Publisher, error) {
	var api s3publish.API

	if deps.NewAPI != nil {
		custom, err := deps.NewAPI(cmd.Context(), tgt.spec, tgt.name)
		if err != nil {
			return nil, err
		}

		api = custom
	} else {
		client, err := newClient(cmd.Context(), deps.ConnectionOptions, tgt)
		if err != nil {
			return nil, err
		}

		api = client
	}

	pub, err := s3publish.New(api, &tgt.cfg)
	if err != nil {
		return nil, fmt.Errorf("publisher: %w", err)
	}

	return pub, nil
}

func newVerifyCmd(opts *options, deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check the manifest and every object it lists in the bucket",
		Long: `Reads the manifest and every listed object of the connection, and checks size, checksum, the content of
each document, and that every flag-set source resolves. It never writes. Exit code 5 when a problem is found.`,
		Args:          jsonArgs(opts, cobra.NoArgs),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return reportError(cmd, opts, runVerify(cmd, opts, deps))
		},
	}
}

func runVerify(cmd *cobra.Command, opts *options, deps *Deps) error {
	err := checkOutput(opts.output)
	if err != nil {
		return err
	}

	tgt, err := resolveTarget(&opts.target, deps)
	if err != nil {
		return withCode(err)
	}

	tgt.cfg.Check = checkFlags

	pub, err := newPublisher(cmd, tgt, deps)
	if err != nil {
		return withCode(err)
	}

	rep, err := pub.Verify(cmd.Context())
	if err != nil {
		return withCode(err)
	}

	err = printReport(cmd.OutOrStdout(), opts.output, rep)
	if err != nil {
		return err
	}

	if !rep.OK() {
		return &helper.ExitError{
			Err:  fmt.Errorf("%w: %d problems", errVerify, len(rep.Problems)),
			Code: helper.ExitVerify,
		}
	}

	return nil
}

var errVerify = errors.New("verification failed")

// withCode attaches the exit code that the kind of error calls for: 2 invalid input, 3 conflict, 4 store call
// failed, 5 bad state in the bucket, 1 anything else.
func withCode(err error) error {
	if errors.As(err, new(*helper.ExitError)) {
		return err
	}

	return &helper.ExitError{Err: err, Code: codeOf(err)}
}

func codeOf(err error) int {
	switch {
	case errors.Is(err, s3publish.ErrConflict):
		return helper.ExitConflict
	case errors.As(err, new(*s3publish.OperationError)):
		return helper.ExitTransport
	case errors.Is(err, s3publish.ErrPreviousManifest), errors.Is(err, s3publish.ErrObjectMismatch),
		errors.Is(err, s3publish.ErrNoManifest):
		return helper.ExitVerify
	case errors.Is(err, ErrUsage):
		return helper.ExitFailure
	case isInvalidInput(err):
		return helper.ExitInvalid
	}

	return helper.ExitFailure
}

// isInvalidInput reports the errors that the caller fixes by changing documents or configuration.
func isInvalidInput(err error) bool {
	for _, target := range []error{
		s3publish.ErrInvalid, s3publish.ErrConfig, s3publish.ErrGeneration, s3publish.ErrChannelMismatch,
		ErrUnknownFile, ErrDuplicateObject, ErrNoDocuments, ErrNotRegular, ErrFileTooLarge, ErrSourceTooLarge,
		ErrNoControlPlane, ErrNotS3, bootstrap.ErrSecretUnresolved,
	} {
		if errors.Is(err, target) {
			return true
		}
	}

	return errors.As(err, new(*bootstrap.Error)) || errors.As(err, new(*connection.SpecError))
}
