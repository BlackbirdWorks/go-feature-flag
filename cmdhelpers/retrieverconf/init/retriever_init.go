package init

import (
	"context"
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

// retrieverFactory defines the signature for retriever factory functions.
type retrieverFactory func(*retrieverconf.RetrieverConf, time.Duration) (retriever.Retriever, error)

// factoryFor returns the factory function of a retriever kind.
func factoryFor(kind retrieverconf.RetrieverKind) (retrieverFactory, bool) {
	switch kind {
	case retrieverconf.GitHubRetriever:
		return createGitHubRetriever, true
	case retrieverconf.GitlabRetriever:
		return createGitlabRetriever, true
	case retrieverconf.BitbucketRetriever:
		return createBitbucketRetriever, true
	case retrieverconf.FileRetriever:
		return createFileRetriever, true
	case retrieverconf.S3Retriever:
		return createS3Retriever, true
	case retrieverconf.HTTPRetriever:
		return createHTTPRetriever, true
	case retrieverconf.GoogleStorageRetriever:
		return createGoogleStorageRetriever, true
	case retrieverconf.KubernetesRetriever:
		return createKubernetesRetriever, true
	case retrieverconf.MongoDBRetriever:
		return createMongoDBRetriever, true
	case retrieverconf.RedisRetriever:
		return createRedisRetriever, true
	case retrieverconf.AzBlobStorageRetriever:
		return createAzBlobStorageRetriever, true
	case retrieverconf.PostgreSQLRetriever:
		return createPostgreSQLRetriever, true
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

// InitRetriever initialize the retriever based on the configuration.
func InitRetriever(
	c *retrieverconf.RetrieverConf, opts ...Option) (retriever.Retriever, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	retrieverTimeout := retrieverconf.DefaultTimeout
	if c.Timeout != 0 {
		retrieverTimeout = time.Duration(c.Timeout) * time.Millisecond
	}
	factory, exists := factoryFor(c.Kind)
	if !exists {
		return nil, fmt.Errorf("invalid retriever: kind \"%s\" is not supported", c.Kind)
	}
	r, err := factory(c, retrieverTimeout)
	if err != nil {
		return nil, err
	}
	if pg, ok := r.(*postgresqlretriever.Retriever); ok && o.postgresPools != nil {
		pg.Pools = o.postgresPools
	}

	return r, nil
}

// Factory functions for each retriever type.
func createGitHubRetriever(
	c *retrieverconf.RetrieverConf, timeout time.Duration) (retriever.Retriever, error) {
	token := c.AuthToken
	if token == "" && c.GithubToken != "" { //nolint: staticcheck
		token = c.GithubToken //nolint: staticcheck
	}

	return &githubretriever.Retriever{
		RepositorySlug: c.RepositorySlug,
		Branch: func() string {
			if c.Branch == "" {
				return retrieverconf.DefaultGitBranch
			}

			return c.Branch
		}(),
		FilePath:       c.Path,
		GithubToken:    token,
		BaseURL:        c.BaseURL,
		Timeout:        timeout,
		AppID:          c.GitHubAppID,
		InstallationID: c.GitHubInstallationID,
		PrivateKey:     c.GitHubAppPrivateKey,
		PrivateKeyFile: c.GitHubAppPrivateKeyFile,
	}, nil
}

func createGitlabRetriever(
	c *retrieverconf.RetrieverConf, timeout time.Duration) (retriever.Retriever, error) {
	return &gitlabretriever.Retriever{
		BaseURL: c.BaseURL,
		Branch: func() string {
			if c.Branch == "" {
				return retrieverconf.DefaultGitBranch
			}

			return c.Branch
		}(),
		FilePath:       c.Path,
		GitlabToken:    c.AuthToken,
		RepositorySlug: c.RepositorySlug,
		Timeout:        timeout,
	}, nil
}

func createBitbucketRetriever(
	c *retrieverconf.RetrieverConf, timeout time.Duration) (retriever.Retriever, error) {
	return &bitbucketretriever.Retriever{
		RepositorySlug: c.RepositorySlug,
		Branch: func() string {
			if c.Branch == "" {
				return retrieverconf.DefaultGitBranch
			}

			return c.Branch
		}(),
		FilePath:       c.Path,
		BitBucketToken: c.AuthToken,
		BaseURL:        c.BaseURL,
		Timeout:        timeout,
	}, nil
}

func createFileRetriever(c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	return &fileretriever.Retriever{Path: c.Path}, nil
}

func createS3Retriever(c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	awsConfig, err := awsConf.LoadDefaultConfig(context.Background())

	return &s3retrieverv2.Retriever{
		Bucket:    c.Bucket,
		Item:      c.Item,
		Prefix:    c.Prefix,
		Pattern:   c.Pattern,
		Recursive: c.Recursive,
		AwsConfig: &awsConfig,
	}, err
}

func createHTTPRetriever(
	c *retrieverconf.RetrieverConf, timeout time.Duration) (retriever.Retriever, error) {
	return &httpretriever.Retriever{
		URL: c.URL,
		Method: func() string {
			if c.HTTPMethod == "" {
				return retrieverconf.DefaultHTTPMethod
			}

			return c.HTTPMethod
		}(),
		Body:           c.HTTPBody,
		Header:         c.HTTPHeaders,
		Timeout:        timeout,
		ClientCertPath: c.HTTPClientCertPath,
		ClientKeyPath:  c.HTTPClientKeyPath,
		CACertPath:     c.HTTPCACertPath,
	}, nil
}

func createGoogleStorageRetriever(
	c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	return &gcstorageretriever.Retriever{Bucket: c.Bucket, Object: c.Object}, nil
}

func createKubernetesRetriever(
	c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	client, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	return &k8sretriever.Retriever{
		Namespace:     c.Namespace,
		ConfigMapName: c.ConfigMap,
		Key:           c.Key,
		ClientConfig:  *client,
	}, nil
}

func createMongoDBRetriever(c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	return &mongodbretriever.Retriever{Database: c.Database, URI: c.URI, Collection: c.Collection}, nil
}

func createRedisRetriever(c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	var options *redis.Options
	if c.RedisOptions != nil {
		options = c.RedisOptions.ToRedisOptions()
	}

	return &redisretriever.Retriever{Options: options, Prefix: c.RedisPrefix}, nil
}

func createAzBlobStorageRetriever(
	c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	return &azblobretriever.Retriever{
		Container:   c.Container,
		Object:      c.Object,
		AccountName: c.AccountName,
		AccountKey:  c.AccountKey,
	}, nil
}

func createPostgreSQLRetriever(
	c *retrieverconf.RetrieverConf, _ time.Duration) (retriever.Retriever, error) {
	return &postgresqlretriever.Retriever{URI: c.URI, Table: c.Table, Columns: c.Columns}, nil
}
