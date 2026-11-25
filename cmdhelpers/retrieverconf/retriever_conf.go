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

var (
	errInvalidRetrieverKind = errors.New("invalid retriever: kind")
)

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
	HTTPHeaders map[string][]string `koanf:"headers" mapstructure:"headers"`
	// RedisOptions is the serializable redis configuration that can be used in JSON/YAML files
	RedisOptions *SerializableRedisOptions `koanf:"redisOptions" mapstructure:"redisOptions"`
	// Columns is used by
	// - the postgresql retriever (it allows to use custom column names)
	Columns                 map[string]string `koanf:"columns"                 mapstructure:"columns"`
	Database                string            `koanf:"database"                mapstructure:"database"`
	ConfigMap               string            `koanf:"configmap"               mapstructure:"configmap"`
	URL                     string            `koanf:"url"                     mapstructure:"url"`
	GitHubAppPrivateKeyFile string            `koanf:"githubappprivatekeyfile" mapstructure:"githubAppPrivateKeyFile"`
	HTTPMethod              string            `koanf:"method"                  mapstructure:"method"`
	HTTPBody                string            `koanf:"body"                    mapstructure:"body"`
	Path                    string            `koanf:"path"                    mapstructure:"path"`
	HTTPClientCertPath      string            `koanf:"clientcertpath"          mapstructure:"clientCertPath"`
	HTTPClientKeyPath       string            `koanf:"clientkeypath"           mapstructure:"clientKeyPath"`
	HTTPCACertPath          string            `koanf:"cacertpath"              mapstructure:"caCertPath"`
	Bucket                  string            `koanf:"bucket"                  mapstructure:"bucket"`
	Object                  string            `koanf:"object"                  mapstructure:"object"`
	Item                    string            `koanf:"item"                    mapstructure:"item"`
	Prefix                  string            `koanf:"prefix"                  mapstructure:"prefix"`
	Pattern                 string            `koanf:"pattern"                 mapstructure:"pattern"`
	GitHubAppPrivateKey     string            `koanf:"githubappprivatekey"     mapstructure:"githubAppPrivateKey"`
	// Deprecated: Please use AuthToken instead
	GithubToken string `koanf:"githubtoken" mapstructure:"githubToken"`
	Namespace   string `koanf:"namespace"   mapstructure:"namespace"`
	Key         string `koanf:"key"         mapstructure:"key"`
	BaseURL     string `koanf:"baseurl"     mapstructure:"baseUrl"`
	AuthToken   string `koanf:"token"       mapstructure:"token"`
	// URI is used by
	// - the postgresql retriever
	// - the mongodb retriever
	URI string `koanf:"uri" mapstructure:"uri"`
	// Table is used by
	// - the postgresql retriever
	Table          string        `koanf:"table"          mapstructure:"table"`
	Branch         string        `koanf:"branch"         mapstructure:"branch"`
	Kind           RetrieverKind `koanf:"kind"           mapstructure:"kind"`
	Collection     string        `koanf:"collection"     mapstructure:"collection"`
	RepositorySlug string        `koanf:"repositoryslug" mapstructure:"repositorySlug"`
	RedisPrefix    string        `koanf:"redisPrefix"    mapstructure:"redisPrefix"`
	AccountName    string        `koanf:"accountname"    mapstructure:"accountName"`
	AccountKey     string        `koanf:"accountkey"     mapstructure:"accountKey"`
	Container      string        `koanf:"container"      mapstructure:"container"`
	// GitHub App authentication fields (GitHub retriever only).
	// Mutually exclusive with token / githubToken.
	// AppID and InstallationID are required; supply the private key either inline
	// (GitHubAppPrivateKey) or as a file path (GitHubAppPrivateKeyFile).
	GitHubAppID          int64 `koanf:"githubappid"          mapstructure:"githubAppId"`
	GitHubInstallationID int64 `koanf:"githubinstallationid" mapstructure:"githubInstallationId"`
	Timeout              int64 `koanf:"timeout"              mapstructure:"timeout"`
	Recursive            bool  `koanf:"recursive"            mapstructure:"recursive"`
}

// IsValid validates the configuration of the retriever.
func (c *RetrieverConf) IsValid() error {
	if err := c.Kind.IsValid(); err != nil {
		return err
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
		if err := c.validateS3Retriever(); err != nil {
			return err
		}
		if c.Bucket == "" {
			return err.NewRetrieverConfError("bucket", string(c.Kind))
		}
	case GoogleStorageRetriever:
		if c.Object == "" {
			return err.NewRetrieverConfError("object", string(c.Kind))
		}
		if c.Bucket == "" {
			return err.NewRetrieverConfError("bucket", string(c.Kind))
		}
	case FileRetriever:
		if c.Path == "" {
			return err.NewRetrieverConfError("path", string(c.Kind))
		}
	default:
		// not a storage retriever
	}

	return nil
}

func (c *RetrieverConf) validateHTTPRetriever() error {
	if c.URL == "" {
		return err.NewRetrieverConfError("url", string(c.Kind))
	}
	if c.HTTPClientCertPath != "" && c.HTTPClientKeyPath == "" {
		return err.NewRetrieverConfError("clientKeyPath", string(c.Kind))
	}
	if c.HTTPClientCertPath == "" && c.HTTPClientKeyPath != "" {
		return err.NewRetrieverConfError("clientCertPath", string(c.Kind))
	}

	return nil
}

// validatePostgreSQLRetriever validates the configuration of the postgresql retriever.
func (c *RetrieverConf) validatePostgreSQLRetriever() error {
	if c.URI == "" {
		return err.NewRetrieverConfError("uri", string(c.Kind))
	}
	if c.Table == "" {
		return err.NewRetrieverConfError("table", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateGitRetriever() error {
	if c.RepositorySlug == "" {
		return err.NewRetrieverConfError("repositorySlug", string(c.Kind))
	}
	if c.Path == "" {
		return err.NewRetrieverConfError("path", string(c.Kind))
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
			return err.NewRetrieverConfError("githubAppId", string(c.Kind))
		}
		if c.GitHubInstallationID == 0 {
			return err.NewRetrieverConfError("githubInstallationId", string(c.Kind))
		}
		if c.GitHubAppPrivateKey == "" && c.GitHubAppPrivateKeyFile == "" {
			return err.NewRetrieverConfError(
				"githubAppPrivateKey or githubAppPrivateKeyFile", string(c.Kind),
			)
		}
	}

	return nil
}

func (c *RetrieverConf) validateKubernetesRetriever() error {
	if c.ConfigMap == "" {
		return err.NewRetrieverConfError("configmap", string(c.Kind))
	}
	if c.Namespace == "" {
		return err.NewRetrieverConfError("namespace", string(c.Kind))
	}
	if c.Key == "" {
		return err.NewRetrieverConfError("key", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateMongoDBRetriever() error {
	if c.Collection == "" {
		return err.NewRetrieverConfError("collection", string(c.Kind))
	}
	if c.Database == "" {
		return err.NewRetrieverConfError("database", string(c.Kind))
	}
	if c.URI == "" {
		return err.NewRetrieverConfError("uri", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateRedisRetriever() error {
	if c.RedisOptions == nil {
		return err.NewRetrieverConfError("redisOptions", string(c.Kind))
	}
	if c.RedisOptions.Addr == "" {
		return err.NewRetrieverConfError("redisOptions.addr", string(c.Kind))
	}

	return nil
}

func (c *RetrieverConf) validateAzBlobStorageRetriever() error {
	if c.AccountName == "" {
		return err.NewRetrieverConfError("accountName", string(c.Kind))
	}
	if c.Container == "" {
		return err.NewRetrieverConfError("container", string(c.Kind))
	}
	if c.Object == "" {
		return err.NewRetrieverConfError("object", string(c.Kind))
	}

	return nil
}

// RetrieverKind is an enum containing all accepted Retriever kind.
type RetrieverKind string

const (
	HTTPRetriever          RetrieverKind = "http"
	GitHubRetriever        RetrieverKind = "github"
	GitlabRetriever        RetrieverKind = "gitlab"
	S3Retriever            RetrieverKind = "s3"
	FileRetriever          RetrieverKind = "file"
	GoogleStorageRetriever RetrieverKind = "googleStorage"
	KubernetesRetriever    RetrieverKind = "configmap"
	MongoDBRetriever       RetrieverKind = "mongodb"
	RedisRetriever         RetrieverKind = "redis"
	BitbucketRetriever     RetrieverKind = "bitbucket"
	AzBlobStorageRetriever RetrieverKind = "azureBlobStorage"
	PostgreSQLRetriever    RetrieverKind = "postgresql"
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
	if err := folderloader.Validate("item", c.Item, c.Prefix, c.Pattern, c.Recursive); err != nil {
		return fmt.Errorf("invalid retriever: %w for kind \"%s\"", err, c.Kind)
	}

	return nil
}
