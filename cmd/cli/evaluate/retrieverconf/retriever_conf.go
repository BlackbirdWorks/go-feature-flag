package retrieverconf

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/blackbirdworks/semaphore/cmdhelpers/err"
	"github.com/blackbirdworks/semaphore/internal/folderloader"
)

var errGitHubTokenAndApp = errors.New(
	"invalid retriever: token and GitHub App fields (githubAppId, githubInstallationId, " +
		"githubAppPrivateKey / githubAppPrivateKeyFile) are mutually exclusive for kind",
)

var errInvalidRetrieverKind = errors.New("invalid retriever: kind")

// The defaults of the retriever configuration.
const (
	// DefaultTimeout is the timeout of a retriever that does not set one.
	DefaultTimeout = 10 * time.Second
	// DefaultHTTPMethod is the method of the HTTP retriever that does not set one.
	DefaultHTTPMethod = http.MethodGet
	// DefaultGitBranch is the branch of the git retrievers that do not set one.
	DefaultGitBranch = "main"
)

// RetrieverConf contains all the field to configure a retriever.
type RetrieverConf struct {
	HTTPHeaders map[string][]string `mapstructure:"headers"`
	// RedisOptions is the serializable redis configuration that can be used in JSON/YAML files
	RedisOptions *SerializableRedisOptions `mapstructure:"redisOptions"`
	// Columns is used by
	// - the postgresql retriever (it allows to use custom column names)
	Columns                 map[string]string `mapstructure:"columns"`
	Database                string            `mapstructure:"database"`
	ConfigMap               string            `mapstructure:"configmap"`
	URL                     string            `mapstructure:"url"`
	GitHubAppPrivateKeyFile string            `mapstructure:"githubAppPrivateKeyFile"`
	HTTPMethod              string            `mapstructure:"method"`
	HTTPBody                string            `mapstructure:"body"`
	Path                    string            `mapstructure:"path"`
	HTTPClientCertPath      string            `mapstructure:"clientCertPath"`
	HTTPClientKeyPath       string            `mapstructure:"clientKeyPath"`
	HTTPCACertPath          string            `mapstructure:"caCertPath"`
	Bucket                  string            `mapstructure:"bucket"`
	Object                  string            `mapstructure:"object"`
	Item                    string            `mapstructure:"item"`
	Prefix                  string            `mapstructure:"prefix"`
	Pattern                 string            `mapstructure:"pattern"`
	GitHubAppPrivateKey     string            `mapstructure:"githubAppPrivateKey"`
	// Deprecated: Please use AuthToken instead
	GithubToken string `mapstructure:"githubToken"`
	Namespace   string `mapstructure:"namespace"`
	Key         string `mapstructure:"key"`
	BaseURL     string `mapstructure:"baseUrl"`
	AuthToken   string `mapstructure:"token"`
	// URI is used by
	// - the postgresql retriever
	// - the mongodb retriever
	URI string `mapstructure:"uri"`
	// Table is used by
	// - the postgresql retriever
	Table          string        `mapstructure:"table"`
	Branch         string        `mapstructure:"branch"`
	Kind           RetrieverKind `mapstructure:"kind"`
	Collection     string        `mapstructure:"collection"`
	RepositorySlug string        `mapstructure:"repositorySlug"`
	RedisPrefix    string        `mapstructure:"redisPrefix"`
	AccountName    string        `mapstructure:"accountName"`
	AccountKey     string        `mapstructure:"accountKey"`
	Container      string        `mapstructure:"container"`
	// GitHub App authentication fields (GitHub retriever only).
	// Mutually exclusive with token / githubToken.
	// AppID and InstallationID are required; supply the private key either inline
	// (GitHubAppPrivateKey) or as a file path (GitHubAppPrivateKeyFile).
	GitHubAppID          int64 `mapstructure:"githubAppId"`
	GitHubInstallationID int64 `mapstructure:"githubInstallationId"`
	Timeout              int64 `mapstructure:"timeout"`
	Recursive            bool  `mapstructure:"recursive"`
}

// IsValid validates the configuration of the retriever.
func (c *RetrieverConf) IsValid() error {
	kindErr := c.Kind.IsValid()
	if kindErr != nil {
		return kindErr
	}

	switch c.Kind {
	case PostgreSQLRetriever:
		return c.validatePostgreSQLRetriever()
	case GitHubRetriever, GitlabRetriever, BitbucketRetriever:
		return c.validateGitRetriever()
	case S3Retriever, GoogleStorageRetriever, FileRetriever:
		return c.validateStorageRetriever()
	case HTTPRetriever:
		return c.validateHTTPRetriever()
	case KubernetesRetriever:
		return c.validateKubernetesRetriever()
	case MongoDBRetriever:
		return c.validateMongoDBRetriever()
	case RedisRetriever:
		return c.validateRedisRetriever()
	case AzBlobStorageRetriever:
		return c.validateAzBlobStorageRetriever()
	default:
		// the other kinds have no setting to validate
	}

	return nil
}

// validateStorageRetriever validates the retrievers of a bucket or of a file.
func (c *RetrieverConf) validateStorageRetriever() error {
	switch c.Kind {
	case S3Retriever:
		validateErr := c.validateS3Retriever()
		if validateErr != nil {
			return validateErr
		}

		if c.Bucket == "" {
			return missingProperty("bucket", string(c.Kind))
		}
	case GoogleStorageRetriever:
		if c.Object == "" {
			return missingProperty("object", string(c.Kind))
		}

		if c.Bucket == "" {
			return missingProperty("bucket", string(c.Kind))
		}
	case FileRetriever:
		if c.Path == "" {
			return missingProperty("path", string(c.Kind))
		}
	case HTTPRetriever,
		GitHubRetriever,
		GitlabRetriever,
		KubernetesRetriever,
		MongoDBRetriever,
		RedisRetriever,
		BitbucketRetriever,
		AzBlobStorageRetriever,
		PostgreSQLRetriever:
		// not a storage retriever
	}

	return nil
}

func (c *RetrieverConf) validateHTTPRetriever() error {
	if c.URL == "" {
		return missingProperty("url", string(c.Kind))
	}

	if c.HTTPClientCertPath != "" && c.HTTPClientKeyPath == "" {
		return missingProperty("clientKeyPath", string(c.Kind))
	}

	if c.HTTPClientCertPath == "" && c.HTTPClientKeyPath != "" {
		return missingProperty("clientCertPath", string(c.Kind))
	}

	return nil
}

// validatePostgreSQLRetriever validates the configuration of the postgresql retriever.
func (c *RetrieverConf) validatePostgreSQLRetriever() error {
	if c.URI == "" {
		return missingProperty("uri", string(c.Kind))
	}

	if c.Table == "" {
		return missingProperty("table", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateGitRetriever() error {
	if c.RepositorySlug == "" {
		return missingProperty("repositorySlug", string(c.Kind))
	}

	if c.Path == "" {
		return missingProperty("path", string(c.Kind))
	}

	if c.Kind == GitHubRetriever {
		return c.validateGitHubAuth()
	}

	return nil
}

// validateGitHubAuth ensures the authentication configuration is consistent.
// A config may use either a static token OR GitHub App credentials, not both.
func (c *RetrieverConf) validateGitHubAuth() error {
	hasToken := c.AuthToken != "" ||
		c.GithubToken != ""
	hasAppFields := c.GitHubAppID != 0 || c.GitHubInstallationID != 0 ||
		c.GitHubAppPrivateKey != "" || c.GitHubAppPrivateKeyFile != ""

	if hasToken && hasAppFields {
		return fmt.Errorf("%w %q", errGitHubTokenAndApp, string(c.Kind))
	}

	if hasAppFields {
		if c.GitHubAppID == 0 {
			return missingProperty("githubAppId", string(c.Kind))
		}

		if c.GitHubInstallationID == 0 {
			return missingProperty("githubInstallationId", string(c.Kind))
		}

		if c.GitHubAppPrivateKey == "" && c.GitHubAppPrivateKeyFile == "" {
			return missingProperty(
				"githubAppPrivateKey or githubAppPrivateKeyFile", string(c.Kind),
			)
		}
	}

	return nil
}

func (c *RetrieverConf) validateKubernetesRetriever() error {
	if c.ConfigMap == "" {
		return missingProperty("configmap", string(c.Kind))
	}

	if c.Namespace == "" {
		return missingProperty("namespace", string(c.Kind))
	}

	if c.Key == "" {
		return missingProperty("key", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateMongoDBRetriever() error {
	if c.Collection == "" {
		return missingProperty("collection", string(c.Kind))
	}

	if c.Database == "" {
		return missingProperty("database", string(c.Kind))
	}

	if c.URI == "" {
		return missingProperty("uri", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateRedisRetriever() error {
	if c.RedisOptions == nil {
		return missingProperty("redisOptions", string(c.Kind))
	}

	if c.RedisOptions.Addr == "" {
		return missingProperty("redisOptions.addr", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateAzBlobStorageRetriever() error {
	if c.AccountName == "" {
		return missingProperty("accountName", string(c.Kind))
	}

	if c.Container == "" {
		return missingProperty("container", string(c.Kind))
	}

	if c.Object == "" {
		return missingProperty("object", string(c.Kind))
	}

	return nil
}

// RetrieverKind is an enum containing all accepted Retriever kind.
type RetrieverKind string

const (
	// HTTPRetriever is the kind of retriever that retrieves flags over HTTP.
	HTTPRetriever RetrieverKind = "http"
	// GitHubRetriever is the kind of retriever that retrieves flags from GitHub repositories.
	GitHubRetriever RetrieverKind = "github"
	// GitlabRetriever is the kind of retriever that retrieves flags from GitLab repositories.
	GitlabRetriever RetrieverKind = "gitlab"
	// S3Retriever is the kind of retriever that retrieves flags from Amazon S3.
	S3Retriever RetrieverKind = "s3"
	// FileRetriever is the kind of retriever that retrieves flags from local files.
	FileRetriever RetrieverKind = "file"
	// GoogleStorageRetriever is the kind of retriever that retrieves flags from Google Cloud Storage.
	GoogleStorageRetriever RetrieverKind = "googleStorage"
	// KubernetesRetriever is the kind of retriever that retrieves flags from Kubernetes resources.
	KubernetesRetriever RetrieverKind = "configmap"
	// MongoDBRetriever is the kind of retriever that retrieves flags from MongoDB.
	MongoDBRetriever RetrieverKind = "mongodb"
	// RedisRetriever is the kind of retriever that retrieves flags from Redis.
	RedisRetriever RetrieverKind = "redis"
	// BitbucketRetriever is the kind of retriever that retrieves flags from Bitbucket.
	BitbucketRetriever RetrieverKind = "bitbucket"
	// AzBlobStorageRetriever is the kind of retriever that retrieves flags from Azure Blob Storage.
	AzBlobStorageRetriever RetrieverKind = "azureBlobStorage"
	// PostgreSQLRetriever is the kind of retriever that retrieves flags from PostgreSQL.
	PostgreSQLRetriever RetrieverKind = "postgresql"
)

// IsValid is checking if the value is part of the enum.
func (r RetrieverKind) IsValid() error {
	switch r {
	case HTTPRetriever, GitHubRetriever, GitlabRetriever, S3Retriever, RedisRetriever,
		FileRetriever, GoogleStorageRetriever, KubernetesRetriever, MongoDBRetriever,
		BitbucketRetriever, AzBlobStorageRetriever, PostgreSQLRetriever:
		return nil
	}

	return fmt.Errorf("%w \"%s\" is not supported", errInvalidRetrieverKind, r)
}

// validateS3Retriever validates the item/prefix settings (no item means folder mode) of the S3 retriever.
func (c *RetrieverConf) validateS3Retriever() error {
	validateErr := folderloader.Validate("item", c.Item, c.Prefix, c.Pattern, c.Recursive)
	if validateErr != nil {
		return fmt.Errorf("invalid retriever: %w for kind \"%s\"", validateErr, c.Kind)
	}

	return nil
}

// missingProperty is the error of a retriever of the kind that has no value for property.
func missingProperty(property, kind string) error {
	return fmt.Errorf("validating the retriever configuration: %w", err.NewRetrieverConfError(property, kind))
}
