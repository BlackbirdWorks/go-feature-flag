package evaluate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate/retrieverconf"
	retrieverinit "github.com/blackbirdworks/semaphore/cmd/cli/evaluate/retrieverconf/init"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	rerr "github.com/blackbirdworks/semaphore/cmdhelpers/err"
	"github.com/blackbirdworks/semaphore/pkgs/stringsx"
	"github.com/blackbirdworks/semaphore/retriever"
)

const (
	// keyValueParts is the number of parts of a "key:value" or "key=value" argument.
	keyValueParts = 2

	evaluateCmdExample = `Examples:

# Evaluate a specific flag using deprecated flag --config
evaluate --config ./config.yaml --flag flag1 --ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using new flag --path
evaluate --kind file --path ./config.yaml --flag flag1 --ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using http retriever
evaluate --kind http --url http://localhost:8080/config.yaml --header 'ContentType: application/json' --header
'X-Auth=Token' --flag flag1 --ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using github retriever
evaluate --kind github --repository-slug thomaspoignant/go-feature-flag --branch master --flag flag1
--ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using gitlab retriever
evaluate --kind gitlab --base-url https://gitlab.com --repository-slug thomaspoignant/go-feature-flag
--branch master --flag flag1 --ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using bitbucket retriever
evaluate --kind bitbucket --repository-slug thomaspoignant/go-feature-flag --branch master --flag flag1
--ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using s3 retriever
evaluate --kind s3 --bucket my-bucket --item my-item.yaml --flag flag1 --ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using gcs retriever
evaluate --kind googleStorage --bucket my-bucket --object my-item.yaml --flag flag1 --ctx
'{"targetingKey": "user-123"}'

# Evaluate a specific flag using configmap retriever
evaluate --kind configmap --namespace default --config-map my-configmap --key my-key.yaml --flag flag1
--ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using mongodb retriever
evaluate --kind mongodb --uri mongodb://localhost:27017 --database my-database --collection my-collection --flag flag1
--ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using azureblob retriever
evaluate --kind azureblob --container my-container --account-name my-account-name --account-key my-account-key
--object my-object --flag flag1 --ctx '{"targetingKey": "user-123"}'

# Evaluate a specific flag using postgres retriever
evaluate --kind postgres --table my-table --column my-column:my-column-type --flag flag1
--ctx '{"targetingKey": "user-123"}'`
)

// HelpVars returns the kong interpolation variables used by [Cmd] help tags.
func HelpVars() kong.Vars {
	return kong.Vars{
		"helpEvalFormat":  "Format of your input file (YAML, JSON or TOML)",
		"helpEvalKind":    "Kind of the configuration file (file, http, redis, gitlab, k8s, ...)",
		"helpEvalConfig":  "Location of your Semaphore local configuration file",
		"helpEvalPath":    "Path to your Semaphore configuration file (local or remote)",
		"helpEvalAuth":    "Authentication token to access your configuration file",
		"helpEvalGHToken": "Authentication token to access your configuration file on GitHub",
		"helpEvalRepo":    "Repository slug to access your configuration file on GitHub",
		"helpEvalBranch":  "Branch to access your configuration file on GitHub",
		"helpEvalBaseURL": "Base URL of your configuration file on Gitlab",
		"helpEvalBucket":  "Bucket of your configuration file on S3",
		"helpEvalItem":    "Item of your configuration file on S3",
		"helpEvalURL":     "URL of your configuration file on HTTP",
		"helpEvalMethod":  "HTTP method to access your configuration file on HTTP",
		"helpEvalBody":    "Http body to access your configuration file on HTTP",
		"helpEvalHeader":  "HTTP header to access your configuration file on HTTP (may be repeated)",
		"helpEvalTimeout": "Timeout in seconds to access your configuration file on HTTP",
		"helpEvalFlag":    "Name of the flag to evaluate, if empty we will return the evaluation of all the flags",
		"helpEvalCtx":     "Evaluation context in JSON format",
		"helpEvalCheck":   "Check only mode - returns the configuration of spanned retriever without evaluating",
		"helpEvalObject":  "Object of your configuration file on GCS",
		"helpEvalNS":      "Namespace of your configuration file on K8s",
		"helpEvalCM":      "Config map of your configuration file on K8s",
		"helpEvalKey":     "Key of your configuration file on K8s",
		"helpEvalURI":     "URI of your configuration file",
		"helpEvalDB":      "Database of your configuration file on MongoDB",
		"helpEvalColl":    "Collection of your configuration file on MongoDB",
		"helpEvalCont":    "Container of your configuration file on Azure Blob Storage",
		"helpEvalAcct":    "Account name of your configuration file on Azure Blob Storage",
		"helpEvalAcctKey": "Account key of your configuration file on Azure Blob Storage",
		"helpEvalTable":   "Postgres table of your configuration file on Postgres",
		"helpEvalColumn":  "Postgres column mapping of your configuration file on Postgres (may be repeated)",
	}
}

// Cmd evaluates feature flags based on configuration and context.
//
//nolint:govet // fieldalignment: field order sets kong help flag order
type Cmd struct {
	Headers        []string `help:"${helpEvalHeader}"  name:"header"             sep:"none"`
	Columns        []string `help:"${helpEvalColumn}"  name:"column"             sep:"none"`
	Format         string   `default:"yaml"            help:"${helpEvalFormat}"  name:"format"       short:"f"`
	Kind           string   `default:"file"            help:"${helpEvalKind}"    name:"kind"         short:"k"`
	Config         string   `help:"${helpEvalConfig}"  hidden:""                 name:"config"       short:"c"`
	Path           string   `help:"${helpEvalPath}"    name:"path"               short:"p"`
	AuthToken      string   `help:"${helpEvalAuth}"    name:"auth-token"`
	GithubToken    string   `help:"${helpEvalGHToken}" hidden:""                 name:"github-token"`
	RepositorySlug string   `help:"${helpEvalRepo}"    name:"repository-slug"`
	Branch         string   `help:"${helpEvalBranch}"  name:"branch"`
	BaseURL        string   `help:"${helpEvalBaseURL}" name:"base-url"`
	Bucket         string   `help:"${helpEvalBucket}"  name:"bucket"`
	Item           string   `help:"${helpEvalItem}"    name:"item"`
	URL            string   `help:"${helpEvalURL}"     name:"url"`
	Method         string   `default:"GET"             help:"${helpEvalMethod}"  name:"method"`
	Body           string   `help:"${helpEvalBody}"    name:"body"`
	EvalFlag       string   `help:"${helpEvalFlag}"    name:"flag"`
	EvalCtx        string   `default:"{}"              help:"${helpEvalCtx}"     name:"ctx"`
	Object         string   `help:"${helpEvalObject}"  name:"object"`
	Namespace      string   `default:"default"         help:"${helpEvalNS}"      name:"namespace"`
	ConfigMap      string   `help:"${helpEvalCM}"      name:"config-map"`
	Key            string   `help:"${helpEvalKey}"     name:"key"`
	URI            string   `help:"${helpEvalURI}"     name:"uri"`
	Database       string   `help:"${helpEvalDB}"      name:"database"`
	Collection     string   `help:"${helpEvalColl}"    name:"collection"`
	Container      string   `help:"${helpEvalCont}"    name:"container"`
	AccountName    string   `help:"${helpEvalAcct}"    name:"account-name"`
	AccountKey     string   `help:"${helpEvalAcctKey}" name:"account-key"`
	Table          string   `help:"${helpEvalTable}"   name:"table"`
	Timeout        int64    `default:"0"               help:"${helpEvalTimeout}" name:"timeout"`
	CheckMode      bool     `help:"${helpEvalCheck}"   name:"check-mode"`
}

// Help returns the detailed examples and description of evaluate for kong's help output.
func (*Cmd) Help() string {
	return "⚙️ Evaluate feature flags based on configuration and context," +
		" if no specific flag requested it will evaluate all flags\n\n" + evaluateCmdExample
}

// Run executes the evaluate command with injected context and output streams.
func (c *Cmd) Run(ctx context.Context, streams *helper.Streams) error {
	if c.Config != "" {
		_, _ = fmt.Fprintln(streams.Stderr, "Flag --config has been deprecated, Use path instead")
	}

	if c.GithubToken != "" {
		_, _ = fmt.Fprintln(streams.Stderr, "Flag --github-token has been deprecated, Use auth-token instead")
	}

	return runEvaluateCmd(ctx, streams, c)
}

// Run parses args against [Cmd] and executes flag evaluation with injected context and output streams.
func Run(ctx context.Context, args []string, streams *helper.Streams) error {
	var runner struct {
		Cmd

		Help helper.HelpFlag `help:"Show context-sensitive help." name:"help" short:"h"`
	}

	parser, err := kong.New(&runner,
		kong.Name("evaluate"),
		kong.Description("⚙️ Evaluate feature flags based on configuration and context"),
		kong.NoDefaultHelp(),
		kong.Writers(streams.Stdout, streams.Stderr),
		kong.ConfigureHelp(kong.HelpOptions{WrapUpperBound: helper.HelpWidth}),
		HelpVars(),
	)
	if err != nil {
		return fmt.Errorf("build evaluate parser: %w", err)
	}

	kctx, exited, err := helper.Parse(parser, args)
	if exited {
		return nil
	}

	if err != nil {
		return fmt.Errorf("%w: %w", helper.ErrUsage, err)
	}

	kctx.BindTo(ctx, (*context.Context)(nil))

	return kctx.Run(streams) //nolint:wrapcheck // Cmd.Run returns user-facing error
}

func runEvaluateCmd(ctx context.Context, streams *helper.Streams, flags *Cmd) error {
	parsedHeaders := parseHTTPHeaders(flags.Headers)
	parsedColumns := parsePostgresColumns(flags.Columns)

	retrieverPath := flags.Path
	if retrieverPath == "" {
		retrieverPath = flags.Config
	}

	retrieverConf := retrieverconf.RetrieverConf{
		Kind:           retrieverconf.RetrieverKind(flags.Kind),
		RepositorySlug: flags.RepositorySlug,
		Branch:         flags.Branch,
		Path:           retrieverPath,
		GithubToken:    flags.GithubToken, //nolint:staticcheck // backward compatibility for deprecated --github-token flag
		AuthToken:      flags.AuthToken,
		BaseURL:        flags.BaseURL,
		Bucket:         flags.Bucket,
		Item:           flags.Item,
		URL:            flags.URL,
		Timeout:        flags.Timeout,
		HTTPMethod:     flags.Method,
		HTTPBody:       flags.Body,
		HTTPHeaders:    parsedHeaders,
		Object:         flags.Object,
		Namespace:      flags.Namespace,
		ConfigMap:      flags.ConfigMap,
		Key:            flags.Key,
		URI:            flags.URI,
		Database:       flags.Database,
		Collection:     flags.Collection,
		Container:      flags.Container,
		AccountName:    flags.AccountName,
		AccountKey:     flags.AccountKey,
		Table:          flags.Table,
		Columns:        parsedColumns,
	}

	err := retrieverConf.IsValid()
	if err != nil {
		rcErr := &rerr.RetrieverConfError{}
		if errors.As(err, &rcErr) {
			return cliMessageError(rcErr.CliErrorMessage())
		}

		return fmt.Errorf("validating the retriever of the command: %w", err)
	}

	if flags.CheckMode {
		return runCheck(streams, &retrieverConf)
	}

	return runEvaluate(ctx, streams, flags.Format, &retrieverConf, flags.EvalFlag, flags.EvalCtx)
}

func runEvaluate(
	ctx context.Context,
	streams *helper.Streams,
	flagFormat string,
	retrieverConf *retrieverconf.RetrieverConf,
	flag, evalCtx string,
) error {
	output := helper.Output{}

	built, err := retrieverinit.Retriever(retrieverConf)
	if err != nil {
		return fmt.Errorf("initializing the retriever of the command: %w", err)
	}

	r := built.Retriever
	logger := slog.Default()

	initErr := tryInitializeStandard(ctx, r, logger)
	if initErr != nil {
		return initErr
	}

	initErr = tryInitializeWithFlagset(ctx, r, logger, stringsx.DefaultFlagSetName)
	if initErr != nil {
		return initErr
	}

	eval := evaluate{
		retriever:     r,
		fileFormat:    flagFormat,
		flag:          flag,
		evaluationCtx: evalCtx,
	}

	result, err := eval.Evaluate(ctx)
	if err != nil {
		return err
	}

	detailed, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the evaluation result: %w", err)
	}

	output.Add(string(detailed), helper.DefaultLevel)

	err = output.PrintLines(streams.Stdout, streams.Stderr)
	if err != nil {
		return fmt.Errorf("print evaluation output: %w", err)
	}

	return nil
}

func runCheck(streams *helper.Streams, retrieverConf *retrieverconf.RetrieverConf) error {
	output := helper.Output{}

	//nolint:musttag,gosec // G117: CLI --check-mode outputs the parsed retriever config as JSON
	detailed, err := json.MarshalIndent(retrieverConf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the retriever configuration: %w", err)
	}

	output.Add(string(detailed), helper.DefaultLevel)

	err = output.PrintLines(streams.Stdout, streams.Stderr)
	if err != nil {
		return fmt.Errorf("print check output: %w", err)
	}

	return nil
}

func tryInitializeStandard(ctx context.Context, r retriever.Retriever, logger *slog.Logger) error {
	if r, ok := r.(retriever.InitializableRetriever); ok {
		err := r.Init(ctx, logger)
		if err != nil {
			return fmt.Errorf("impossible to init retriever: %w", err)
		}
	}

	return nil
}

func tryInitializeWithFlagset(
	ctx context.Context, r retriever.Retriever, logger *slog.Logger, flagset string) error {
	if r, ok := r.(retriever.InitializableRetrieverWithFlagset); ok {
		err := r.Init(ctx, logger, &flagset)
		if err != nil {
			return fmt.Errorf("impossible to init flagset retriever: %w", err)
		}
	}

	return nil
}

func parseHTTPHeaders(headers []string) map[string][]string {
	result := make(map[string][]string)

	for _, h := range headers {
		parts := strings.SplitN(h, ":", keyValueParts)
		if len(parts) != keyValueParts {
			parts = strings.SplitN(h, "=", keyValueParts)
		}

		if len(parts) != keyValueParts {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		result[key] = append(result[key], val)
	}

	return result
}

func parsePostgresColumns(columns []string) map[string]string {
	result := make(map[string]string)

	for _, c := range columns {
		parts := strings.SplitN(c, ":", keyValueParts)
		if len(parts) != keyValueParts {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		result[key] = val
	}

	return result
}

// cliMessageError is an error whose text is the message to show on the command line.
type cliMessageError string

func (e cliMessageError) Error() string { return string(e) }
