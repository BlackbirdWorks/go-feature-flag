package init

import (
	"context"
	"errors"
	"fmt"
	"time"

	awsConf "github.com/aws/aws-sdk-go-v2/config"
	"github.com/redis/go-redis/v9"
	"k8s.io/client-go/rest"

	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/retriever"
	azblobretriever "github.com/blackbirdworks/semaphore/retriever/azblobstorageretriever"
	"github.com/blackbirdworks/semaphore/retriever/bitbucketretriever"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/retriever/gcstorageretriever"
	"github.com/blackbirdworks/semaphore/retriever/githubretriever"
	"github.com/blackbirdworks/semaphore/retriever/gitlabretriever"
	"github.com/blackbirdworks/semaphore/retriever/httpretriever"
	"github.com/blackbirdworks/semaphore/retriever/k8sretriever"
	"github.com/blackbirdworks/semaphore/retriever/mongodbretriever"
	"github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
	"github.com/blackbirdworks/semaphore/retriever/redisretriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
)

var (
	errInvalidRetrieverKind = errors.New("invalid retriever: kind")
)

// retrieverFactory defines the signature for retriever factory functions.
type retrieverFactory func(*retrieverconf.RetrieverConf, time.Duration) (retriever.Retriever, error)

// wrapFactory adapts a concrete retriever constructor into a retrieverFactory.
func wrapFactory[T retriever.Retriever](
	fn func(*retrieverconf.RetrieverConf, time.Duration) (T, error),
) retrieverFactory {
	return func(c *retrieverconf.RetrieverConf, timeout time.Duration) (retriever.Retriever, error) {
		return fn(c, timeout)
	}
}

// factoryFor returns the factory function of a retriever kind.
func factoryFor(kind retrieverconf.RetrieverKind) (retrieverFactory, bool) {
	switch kind {
	case retrieverconf.GitHubRetriever:
		return wrapFactory(createGitHubRetriever), true
	case retrieverconf.GitlabRetriever:
		return wrapFactory(createGitlabRetriever), true
	case retrieverconf.BitbucketRetriever:
		return wrapFactory(createBitbucketRetriever), true
	case retrieverconf.FileRetriever:
		return wrapFactory(createFileRetriever), true
	case retrieverconf.S3Retriever:
		return wrapFactory(createS3Retriever), true
	case retrieverconf.HTTPRetriever:
		return wrapFactory(createHTTPRetriever), true
	case retrieverconf.GoogleStorageRetriever:
		return wrapFactory(createGoogleStorageRetriever), true
	case retrieverconf.KubernetesRetriever:
		return wrapFactory(createKubernetesRetriever), true
	case retrieverconf.MongoDBRetriever:
		return wrapFactory(createMongoDBRetriever), true
	case retrieverconf.RedisRetriever:
		return wrapFactory(createRedisRetriever), true
	case retrieverconf.AzBlobStorageRetriever:
		return wrapFactory(createAzBlobStorageRetriever), true
	case retrieverconf.PostgreSQLRetriever:
		return wrapFactory(createPostgreSQLRetriever), true
	default:
		return nil, false
	}
}

// Option customizes InitRetriever.
type Option func(*options)

type options struct {
	postgresPools *postgresqlretriever.PoolRegistry
}

// WithPostgresPools makes the PostgreSQL retrievers that InitRetriever creates share the connection pools
// of pools (a registry owned by the caller, e.g. the relay proxy, which shares them between its flag sets).
// Without it each PostgreSQL retriever owns its pool.
func WithPostgresPools(pools *postgresqlretriever.PoolRegistry) Option {
	return func(o *options) { o.postgresPools = pools }
}

// Retriever initialize the retriever based on the configuration.
func Retriever(
	conf *retrieverconf.RetrieverConf, opts ...Option) (retriever.Retriever, error) {
	var parsedOpts options
	for _, opt := range opts {
		opt(&parsedOpts)
	}

	retrieverTimeout := retrieverconf.DefaultTimeout
	if conf.Timeout != 0 {
		retrieverTimeout = time.Duration(conf.Timeout) * time.Millisecond
	}

	factory, exists := factoryFor(conf.Kind)
	if !exists {
		return nil, fmt.Errorf("%w \"%s\" is not supported", errInvalidRetrieverKind, conf.Kind)
	}

	r, err := factory(conf, retrieverTimeout)
	if err != nil {
		return nil, err
	}

	if pg, ok := r.(*postgresqlretriever.Retriever); ok && parsedOpts.postgresPools != nil {
		pg.Pools = parsedOpts.postgresPools
	}

	return r, nil
}

// Factory functions for each retriever type.
func createGitHubRetriever(
	conf *retrieverconf.RetrieverConf, timeout time.Duration) (*githubretriever.Retriever, error) {
	token := conf.AuthToken
	if token == "" && conf.GithubToken != "" { //nolint:staticcheck // SA1019: the deprecated setting is still supported
		token = conf.GithubToken //nolint:staticcheck // SA1019: the deprecated setting is still supported
	}

	return &githubretriever.Retriever{
		RepositorySlug: conf.RepositorySlug,
		Branch: func() string {
			if conf.Branch == "" {
				return retrieverconf.DefaultGitBranch
			}

			return conf.Branch
		}(),
		FilePath:       conf.Path,
		GithubToken:    token,
		BaseURL:        conf.BaseURL,
		Timeout:        timeout,
		AppID:          conf.GitHubAppID,
		InstallationID: conf.GitHubInstallationID,
		PrivateKey:     conf.GitHubAppPrivateKey,
		PrivateKeyFile: conf.GitHubAppPrivateKeyFile,
	}, nil
}

func createGitlabRetriever(
	conf *retrieverconf.RetrieverConf, timeout time.Duration) (*gitlabretriever.Retriever, error) {
	return &gitlabretriever.Retriever{
		BaseURL: conf.BaseURL,
		Branch: func() string {
			if conf.Branch == "" {
				return retrieverconf.DefaultGitBranch
			}

			return conf.Branch
		}(),
		FilePath:       conf.Path,
		GitlabToken:    conf.AuthToken,
		RepositorySlug: conf.RepositorySlug,
		Timeout:        timeout,
	}, nil
}

func createBitbucketRetriever(
	conf *retrieverconf.RetrieverConf, timeout time.Duration) (*bitbucketretriever.Retriever, error) {
	return &bitbucketretriever.Retriever{
		RepositorySlug: conf.RepositorySlug,
		Branch: func() string {
			if conf.Branch == "" {
				return retrieverconf.DefaultGitBranch
			}

			return conf.Branch
		}(),
		FilePath:       conf.Path,
		BitBucketToken: conf.AuthToken,
		BaseURL:        conf.BaseURL,
		Timeout:        timeout,
	}, nil
}

func createFileRetriever(c *retrieverconf.RetrieverConf, _ time.Duration) (*fileretriever.Retriever, error) {
	return &fileretriever.Retriever{Path: c.Path}, nil
}

func createS3Retriever(conf *retrieverconf.RetrieverConf, _ time.Duration) (*s3retrieverv2.Retriever, error) {
	awsConfig, err := awsConf.LoadDefaultConfig(context.Background())
	if err != nil {
		return nil, fmt.Errorf("load AWS default config: %w", err)
	}

	return &s3retrieverv2.Retriever{
		Bucket:    conf.Bucket,
		Item:      conf.Item,
		Prefix:    conf.Prefix,
		Pattern:   conf.Pattern,
		Recursive: conf.Recursive,
		AwsConfig: &awsConfig,
	}, nil
}

func createHTTPRetriever(
	conf *retrieverconf.RetrieverConf, timeout time.Duration) (*httpretriever.Retriever, error) {
	return &httpretriever.Retriever{
		URL: conf.URL,
		Method: func() string {
			if conf.HTTPMethod == "" {
				return retrieverconf.DefaultHTTPMethod
			}

			return conf.HTTPMethod
		}(),
		Body:           conf.HTTPBody,
		Header:         conf.HTTPHeaders,
		Timeout:        timeout,
		ClientCertPath: conf.HTTPClientCertPath,
		ClientKeyPath:  conf.HTTPClientKeyPath,
		CACertPath:     conf.HTTPCACertPath,
	}, nil
}

func createGoogleStorageRetriever(
	c *retrieverconf.RetrieverConf, _ time.Duration) (*gcstorageretriever.Retriever, error) {
	return &gcstorageretriever.Retriever{Bucket: c.Bucket, Object: c.Object}, nil
}

func createKubernetesRetriever(
	conf *retrieverconf.RetrieverConf, _ time.Duration) (*k8sretriever.Retriever, error) {
	client, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("load in-cluster kubernetes config: %w", err)
	}

	return &k8sretriever.Retriever{
		Namespace:     conf.Namespace,
		ConfigMapName: conf.ConfigMap,
		Key:           conf.Key,
		ClientConfig:  *client,
	}, nil
}

func createMongoDBRetriever(c *retrieverconf.RetrieverConf, _ time.Duration) (*mongodbretriever.Retriever, error) {
	return &mongodbretriever.Retriever{Database: c.Database, URI: c.URI, Collection: c.Collection}, nil
}

func createRedisRetriever(conf *retrieverconf.RetrieverConf, _ time.Duration) (*redisretriever.Retriever, error) {
	var options *redis.Options
	if conf.RedisOptions != nil {
		options = conf.RedisOptions.ToRedisOptions()
	}

	return &redisretriever.Retriever{Options: options, Prefix: conf.RedisPrefix}, nil
}

func createAzBlobStorageRetriever(
	c *retrieverconf.RetrieverConf, _ time.Duration) (*azblobretriever.Retriever, error) {
	return &azblobretriever.Retriever{
		Container:   c.Container,
		Object:      c.Object,
		AccountName: c.AccountName,
		AccountKey:  c.AccountKey,
	}, nil
}

func createPostgreSQLRetriever(
	c *retrieverconf.RetrieverConf, _ time.Duration) (*postgresqlretriever.Retriever, error) {
	return &postgresqlretriever.Retriever{URI: c.URI, Table: c.Table, Columns: c.Columns}, nil
}
