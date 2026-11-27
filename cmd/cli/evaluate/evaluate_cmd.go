package evaluate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

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

	evaluateCmdExample = `
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
--ctx '{"targetingKey": "user-123"}'
`
)

type evaluateFlags struct {
	evalFlagFormat string
	kind           string
	evalConfigFile string
	path           string
	authToken      string
	githubToken    string
	repositorySlug string
	branch         string
	baseURL        string
	bucket         string
	item           string
	url            string
	method         string
	body           string
	object         string
	namespace      string
	configMap      string
	key            string
	uri            string
	database       string
	collection     string
	container      string
	accountName    string
	accountKey     string
	table          string
	evalFlag       string
	evalCtx        string
	headers        []string
	columns        []string
	timeout        int64
	checkMode      bool
}

// NewEvaluateCmd returns the command that evaluates a flag for a given context.
func NewEvaluateCmd() *cobra.Command {
	// Flag values are local to each command so several commands can exist (and run) at once.
	flags := &evaluateFlags{}

	evaluateCmd := &cobra.Command{
		Use:     "evaluate",
		Short:   "⚙️ Evaluate feature flags based on configuration and context",
		Example: evaluateCmdExample,
		Long: "⚙️ Evaluate feature flags based on configuration and context," +
			" if no specific flag requested it will evaluate all flags",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEvaluateCmd(cmd, args, flags)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	registerEvaluateFlags(evaluateCmd, flags)

	return evaluateCmd
}

func registerEvaluateFlags(cmd *cobra.Command, flags *evaluateFlags) {
	cmd.Flags().StringVarP(&flags.evalFlagFormat,
		"format", "f", "yaml", "Format of your input file (YAML, JSON or TOML)")
	cmd.Flags().StringVarP(&flags.kind,
		"kind", "k", "file", "Kind of the configuration file (file, http, redis, gitlab, k8s, ...)")
	cmd.Flags().StringVarP(&flags.evalConfigFile,
		"config", "c", "", "Location of your Semaphore local configuration file")
	cmd.Flags().StringVarP(&flags.path, "path", "p", "",
		"Path to your Semaphore configuration file (local or remote)")
	cmd.Flags().StringVar(&flags.authToken,
		"auth-token", "", "Authentication token to access your configuration file")
	cmd.Flags().StringVar(&flags.githubToken,
		"github-token", "", "Authentication token to access your configuration file on GitHub")
	cmd.Flags().StringVar(&flags.repositorySlug,
		"repository-slug", "", "Repository slug to access your configuration file on GitHub")
	cmd.Flags().StringVar(&flags.branch,
		"branch", "", "Branch to access your configuration file on GitHub")
	cmd.Flags().StringVar(&flags.baseURL,
		"base-url", "", "Base URL of your configuration file on Gitlab")
	cmd.Flags().StringVar(&flags.bucket,
		"bucket", "", "Bucket of your configuration file on S3")
	cmd.Flags().StringVar(&flags.item,
		"item", "", "Item of your configuration file on S3")
	cmd.Flags().StringVar(&flags.url,
		"url", "", "URL of your configuration file on HTTP")
	cmd.Flags().StringVar(&flags.method,
		"method", "GET", "HTTP method to access your configuration file on HTTP")
	cmd.Flags().StringVar(&flags.body,
		"body", "", "Http body to access your configuration file on HTTP")
	cmd.Flags().StringArrayVar(&flags.headers,
		"header", nil,
		"HTTP header to access your configuration file on HTTP (may be repeated). "+
			"See example of `evaluate` command for usages")
	cmd.Flags().Int64Var(&flags.timeout,
		"timeout", 0, "Timeout in seconds to access your configuration file on HTTP")
	cmd.Flags().StringVar(&flags.evalFlag,
		"flag", "",
		"Name of the flag to evaluate, if empty we will return the evaluation of all the flags")
	cmd.Flags().StringVar(&flags.evalCtx,
		"ctx", "{}", "Evaluation context in JSON format")
	cmd.Flags().BoolVar(&flags.checkMode,
		"check-mode", false,
		"Check only mode - when set, the command will not perform any evaluation and returns "+
			"the configuration of spanned retriever")
	cmd.Flags().StringVar(&flags.object,
		"object", "", "Object of your configuration file on GCS")
	cmd.Flags().StringVar(&flags.namespace,
		"namespace", "default", "Namespace of your configuration file on K8s")
	cmd.Flags().StringVar(&flags.configMap,
		"config-map", "", "Config map of your configuration file on K8s")
	cmd.Flags().StringVar(&flags.key,
		"key", "", "Key of your configuration file on K8s")
	cmd.Flags().StringVar(&flags.uri,
		"uri", "", "URI of your configuration file")
	cmd.Flags().StringVar(&flags.database,
		"database", "", "Database of your configuration file on MongoDB")
	cmd.Flags().StringVar(&flags.collection,
		"collection", "", "Collection of your configuration file on MongoDB")
	cmd.Flags().StringVar(&flags.container,
		"container", "", "Container of your configuration file on Azure Blob Storage")
	cmd.Flags().StringVar(&flags.accountName,
		"account-name", "", "Account name of your configuration file on Azure Blob Storage")
	cmd.Flags().StringVar(&flags.accountKey,
		"account-key", "", "Account key of your configuration file on Azure Blob Storage")
	cmd.Flags().StringVar(&flags.table,
		"table", "", "Postgres table of your configuration file on Postgres")
	cmd.Flags().StringArrayVar(&flags.columns,
		"column", nil,
		"Postgres column mapping of your configuration file on Postgres (may be repeated)")
	_ = cmd.Flags().MarkDeprecated("github-token", "Use auth-token instead")
	_ = cmd.Flags().MarkDeprecated("config", "Use path instead")
}

func runEvaluateCmd(cmd *cobra.Command, args []string, flags *evaluateFlags) error {
	parsedHeaders := parseHTTPHeaders(flags.headers)
	parsedColumns := parsePostgresColumns(flags.columns)

	retrieverPath := flags.path
	if retrieverPath == "" {
		retrieverPath = flags.evalConfigFile
	}

	retrieverConf := retrieverconf.RetrieverConf{
		Kind:           retrieverconf.RetrieverKind(flags.kind),
		RepositorySlug: flags.repositorySlug,
		Branch:         flags.branch,
		Path:           retrieverPath,
		GithubToken:    flags.githubToken, //nolint:staticcheck // backward compatibility for deprecated --github-token flag
		AuthToken:      flags.authToken,
		BaseURL:        flags.baseURL,
		Bucket:         flags.bucket,
		Item:           flags.item,
		URL:            flags.url,
		Timeout:        flags.timeout,
		HTTPMethod:     flags.method,
		HTTPBody:       flags.body,
		HTTPHeaders:    parsedHeaders,
		Object:         flags.object,
		Namespace:      flags.namespace,
		ConfigMap:      flags.configMap,
		Key:            flags.key,
		URI:            flags.uri,
		Database:       flags.database,
		Collection:     flags.collection,
		Container:      flags.container,
		AccountName:    flags.accountName,
		AccountKey:     flags.accountKey,
		Table:          flags.table,
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

	if flags.checkMode {
		return runCheck(cmd, &retrieverConf)
	}

	return runEvaluate(cmd, args, flags.evalFlagFormat, &retrieverConf, flags.evalFlag, flags.evalCtx)
}

func runEvaluate(
	cmd *cobra.Command,
	_ []string,
	flagFormat string,
	retrieverConf *retrieverconf.RetrieverConf,
	flag, ctx string) error {
	output := helper.Output{}

	built, err := retrieverinit.Retriever(retrieverConf)
	if err != nil {
		return fmt.Errorf("initializing the retriever of the command: %w", err)
	}

	r := built.Retriever

	logger := slog.Default()

	tryInitializeStandardErr := tryInitializeStandard(
		context.Background(),
		r,
		logger,
	)
	if tryInitializeStandardErr != nil {
		return tryInitializeStandardErr
	}

	tryInitializeWithFlagsetErr := tryInitializeWithFlagset(
		context.Background(),
		r,
		logger,
		stringsx.DefaultFlagSetName,
	)
	if tryInitializeWithFlagsetErr != nil {
		return tryInitializeWithFlagsetErr
	}

	eval := evaluate{
		retriever:     r,
		fileFormat:    flagFormat,
		flag:          flag,
		evaluationCtx: ctx,
	}

	result, err := eval.Evaluate()
	if err != nil {
		return err
	}

	detailed, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the evaluation result: %w", err)
	}

	output.Add(string(detailed), helper.DefaultLevel)
	output.PrintLines(cmd)

	return nil
}

func runCheck(
	cmd *cobra.Command,
	retrieverConf *retrieverconf.RetrieverConf) error {
	output := helper.Output{}

	//nolint:musttag,gosec // G117: CLI --check-mode outputs the parsed retriever config as JSON
	detailed, err := json.MarshalIndent(retrieverConf, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the retriever configuration: %w", err)
	}

	output.Add(string(detailed), helper.DefaultLevel)
	output.PrintLines(cmd)

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
