package init

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/bitbucketretriever"
	"github.com/blackbirdworks/semaphore/retriever/fileretriever"
	"github.com/blackbirdworks/semaphore/retriever/gcstorageretriever"
	"github.com/blackbirdworks/semaphore/retriever/githubretriever"
	"github.com/blackbirdworks/semaphore/retriever/gitlabretriever"
	"github.com/blackbirdworks/semaphore/retriever/httpretriever"
	"github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
	"github.com/blackbirdworks/semaphore/retriever/redisretriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	expectedRedisretrieverRetriever = "expected *redisretriever.Retriever"
	httpsGofeatureflagOrgMy         = "https://gofeatureflag.org/my-flag-test.yaml"
	main                            = "main"
	myBucketName                    = "my-bucket-name"
	test                            = "test:"
)

type initRetrieverCase struct {
	want                   retriever.Retriever
	wantType               retriever.Retriever
	conf                   *retrieverconf.RetrieverConf
	wantErr                assert.ErrorAssertionFunc
	name                   string
	skipCompleteValidation bool
}

func runInitRetrieverTests(t *testing.T, tests []initRetrieverCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Retriever(tt.conf)
			tt.wantErr(t, err)

			if err == nil {
				assert.IsType(t, tt.wantType, got)

				if !tt.skipCompleteValidation {
					assert.Equal(t, tt.want, got)
				}
			}
		})
	}
}

func Test_InitRetriever(t *testing.T) {
	t.Parallel()

	runInitRetrieverTests(t, []initRetrieverCase{
		{
			name:    "Convert Github Retriever",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           fixtures.GitHub,
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				Path:           fixtures.FlagConfigYAMLPath,
				Timeout:        20,
			},
			want: &githubretriever.Retriever{
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				Branch:         main,
				FilePath:       fixtures.FlagConfigYAMLPath,
				GithubToken:    "",
				Timeout:        20 * time.Millisecond,
			},
			wantType: &githubretriever.Retriever{},
		},
		{
			name:    "Convert Github Retriever with token",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           fixtures.GitHub,
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				Path:           fixtures.FlagConfigYAMLPath,
				Timeout:        20,
				AuthToken:      fixtures.Xxx,
			},
			want: &githubretriever.Retriever{
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				Branch:         main,
				FilePath:       fixtures.FlagConfigYAMLPath,
				GithubToken:    fixtures.Xxx,
				Timeout:        20 * time.Millisecond,
			},
			wantType: &githubretriever.Retriever{},
		},
		{
			name:    "Convert Github Retriever with deprecated token",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           fixtures.GitHub,
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				Path:           fixtures.FlagConfigYAMLPath,
				Timeout:        20,
				GithubToken:    fixtures.Xxx, //nolint:staticcheck // SA1019: the test covers the deprecated setting
			},
			want: &githubretriever.Retriever{
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				Branch:         main,
				FilePath:       fixtures.FlagConfigYAMLPath,
				GithubToken:    fixtures.Xxx,
				Timeout:        20 * time.Millisecond,
			},
			wantType: &githubretriever.Retriever{},
		},
		{
			name:    "Convert Github Retriever with BaseURL",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           fixtures.GitHub,
				RepositorySlug: fixtures.RepoMyOrg,
				Path:           fixtures.ConfigFlagsYAML,
				Timeout:        20,
				AuthToken:      fixtures.Xxx,
				BaseURL:        "https://github.acme.com/api/v3",
			},
			want: &githubretriever.Retriever{
				RepositorySlug: fixtures.RepoMyOrg,
				Branch:         main,
				FilePath:       fixtures.ConfigFlagsYAML,
				GithubToken:    fixtures.Xxx,
				BaseURL:        "https://github.acme.com/api/v3",
				Timeout:        20 * time.Millisecond,
			},
			wantType: &githubretriever.Retriever{},
		},
		{
			name:    "Convert Github Retriever with BaseURL and trailing slash",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           fixtures.GitHub,
				RepositorySlug: fixtures.RepoMyOrg,
				Path:           fixtures.ConfigFlagsYAML,
				Timeout:        20,
				BaseURL:        "https://github.acme.com/api/v3/",
			},
			want: &githubretriever.Retriever{
				RepositorySlug: fixtures.RepoMyOrg,
				Branch:         main,
				FilePath:       fixtures.ConfigFlagsYAML,
				GithubToken:    "",
				BaseURL:        "https://github.acme.com/api/v3/",
				Timeout:        20 * time.Millisecond,
			},
			wantType: &githubretriever.Retriever{},
		},
		{
			name:    "Convert Gitlab Retriever",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           "gitlab",
				BaseURL:        "http://localhost",
				Path:           "flag-config.yaml",
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				Timeout:        20,
			},
			want: &gitlabretriever.Retriever{
				BaseURL:        "http://localhost",
				Branch:         main,
				FilePath:       "flag-config.yaml",
				RepositorySlug: fixtures.RepoGoFeatureFlag,
				GitlabToken:    "",
				Timeout:        20 * time.Millisecond,
			},
			wantType: &gitlabretriever.Retriever{},
		},
	})
}

func Test_InitRetriever_StorageAndHTTP(t *testing.T) {
	t.Parallel()

	runInitRetrieverTests(t, []initRetrieverCase{
		{
			name:    "Convert File Retriever",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind: "file",
				Path: fixtures.FlagConfigYAMLPath,
			},
			want:     &fileretriever.Retriever{Path: fixtures.FlagConfigYAMLPath},
			wantType: &fileretriever.Retriever{},
		},
		{
			name:    "Convert S3 Retriever",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:   "s3",
				Bucket: myBucketName,
				Item:   fixtures.FlagConfigYAMLPath,
			},
			want: &s3retrieverv2.Retriever{
				Bucket: myBucketName,
				Item:   fixtures.FlagConfigYAMLPath,
			},
			wantType:               &s3retrieverv2.Retriever{},
			skipCompleteValidation: true,
		},
		{
			name:    "Convert S3 Retriever with prefix",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:      "s3",
				Bucket:    myBucketName,
				Prefix:    "flags/",
				Pattern:   "*.yaml",
				Recursive: true,
			},
			want: &s3retrieverv2.Retriever{
				Bucket:    myBucketName,
				Prefix:    "flags/",
				Pattern:   "*.yaml",
				Recursive: true,
			},
			wantType:               &s3retrieverv2.Retriever{},
			skipCompleteValidation: true,
		},
		{
			name:    "Convert HTTP Retriever",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind: "http",
				URL:  httpsGofeatureflagOrgMy,
			},
			want: &httpretriever.Retriever{
				URL:     httpsGofeatureflagOrgMy,
				Method:  http.MethodGet,
				Body:    "",
				Header:  nil,
				Timeout: 10000000000,
			},
			wantType: &httpretriever.Retriever{},
		},
		{
			name:    "Convert HTTP Retriever with client certificate",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:               "http",
				URL:                httpsGofeatureflagOrgMy,
				HTTPClientCertPath: "/certs/client.crt",
				HTTPClientKeyPath:  "/certs/client.key",
				HTTPCACertPath:     "/certs/ca.crt",
			},
			want: &httpretriever.Retriever{
				URL:            httpsGofeatureflagOrgMy,
				Method:         http.MethodGet,
				Body:           "",
				Header:         nil,
				Timeout:        10000000000,
				ClientCertPath: "/certs/client.crt",
				ClientKeyPath:  "/certs/client.key",
				CACertPath:     "/certs/ca.crt",
			},
			wantType: &httpretriever.Retriever{},
		},
		{
			name:    "Convert Google storage Retriever",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:   "googleStorage",
				Bucket: myBucketName,
				Object: fixtures.FlagConfigYAMLPath,
			},
			want: &gcstorageretriever.Retriever{
				Bucket:  myBucketName,
				Object:  fixtures.FlagConfigYAMLPath,
				Options: nil,
			},
			wantType: &gcstorageretriever.Retriever{},
		},
		{
			name:    "Convert unknown Retriever",
			wantErr: assert.Error,
			conf: &retrieverconf.RetrieverConf{
				Kind: "unknown",
			},
		},
		{
			name:    "Convert Bitbucket Retriever default branch",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           "bitbucket",
				RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
				Path:           fixtures.FlagsConfigGoffYAML,
				AuthToken:      fixtures.BitbucketTokenEnv,
				BaseURL:        fixtures.BitbucketAPIURL,
			},
			want: &bitbucketretriever.Retriever{
				RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
				Branch:         main,
				FilePath:       fixtures.FlagsConfigGoffYAML,
				BitBucketToken: fixtures.BitbucketTokenEnv,
				BaseURL:        fixtures.BitbucketAPIURL,
				Timeout:        10000000000,
			},
			wantType: &bitbucketretriever.Retriever{},
		},
		{
			name:    "Convert Bitbucket Retriever branch specified",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:           "bitbucket",
				Branch:         "develop",
				RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
				Path:           fixtures.FlagsConfigGoffYAML,
				AuthToken:      fixtures.BitbucketTokenEnv,
				BaseURL:        fixtures.BitbucketAPIURL,
			},
			want: &bitbucketretriever.Retriever{
				RepositorySlug: fixtures.RepoGoFeatureFlagConfig,
				Branch:         "develop",
				FilePath:       fixtures.FlagsConfigGoffYAML,
				BitBucketToken: fixtures.BitbucketTokenEnv,
				BaseURL:        fixtures.BitbucketAPIURL,
				Timeout:        10000000000,
			},
			wantType: &bitbucketretriever.Retriever{},
		},
		{
			name:    "Convert Postgres Retriever",
			wantErr: assert.NoError,
			conf: &retrieverconf.RetrieverConf{
				Kind:    "postgresql",
				URI:     "postgresql://user:password@localhost:5432/database",
				Table:   fixtures.FlagsDir,
				Columns: map[string]string{"flagset": "settings"},
			},
			want: &postgresqlretriever.Retriever{
				URI:     "postgresql://user:password@localhost:5432/database",
				Table:   fixtures.FlagsDir,
				Columns: map[string]string{"flagset": "settings"},
			},
			wantType: &postgresqlretriever.Retriever{},
		},
	})
}

func Test_InitRetriever_Redis(t *testing.T) {
	t.Parallel()

	tests := []struct {
		conf              *retrieverconf.RetrieverConf
		validateRetriever func(t *testing.T, r retriever.Retriever)
		name              string
		wantErr           bool
	}{
		{
			name: "Redis with RedisOptions",
			conf: &retrieverconf.RetrieverConf{
				Kind: retrieverconf.RedisRetriever,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr:     "localhost:6379",
					Password: "secret",
					DB:       1,
				},
				RedisPrefix: test,
			},
			wantErr: false,
			validateRetriever: func(t *testing.T, r retriever.Retriever) {
				t.Helper()

				redisRet, ok := r.(*redisretriever.Retriever)
				require.True(t, ok, expectedRedisretrieverRetriever)
				assert.Equal(t, "localhost:6379", redisRet.Options.Addr)
				assert.Equal(t, "secret", redisRet.Options.Password)
				assert.Equal(t, 1, redisRet.Options.DB)
				assert.Equal(t, test, redisRet.Prefix)
			},
		},
		{
			name: "Redis with SerializableRedisOptions with username",
			conf: &retrieverconf.RetrieverConf{
				Kind: retrieverconf.RedisRetriever,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr:     "redis.example.com:6380",
					Password: "newsecret",
					DB:       2,
					Username: "admin",
				},
				RedisPrefix: "flags:",
			},
			wantErr: false,
			validateRetriever: func(t *testing.T, r retriever.Retriever) {
				t.Helper()

				redisRet, ok := r.(*redisretriever.Retriever)
				require.True(t, ok, expectedRedisretrieverRetriever)
				assert.Equal(t, "redis.example.com:6380", redisRet.Options.Addr)
				assert.Equal(t, "newsecret", redisRet.Options.Password)
				assert.Equal(t, 2, redisRet.Options.DB)
				assert.Equal(t, "admin", redisRet.Options.Username)
				assert.Equal(t, "flags:", redisRet.Prefix)
			},
		},
		{
			name: "Redis with RedisOptions password",
			conf: &retrieverconf.RetrieverConf{
				Kind: retrieverconf.RedisRetriever,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr:     "new:6380",
					Password: "new-secret",
				},
				RedisPrefix: test,
			},
			wantErr: false,
			validateRetriever: func(t *testing.T, r retriever.Retriever) {
				t.Helper()

				redisRet, ok := r.(*redisretriever.Retriever)
				require.True(t, ok, expectedRedisretrieverRetriever)
				assert.Equal(t, "new:6380", redisRet.Options.Addr)
				assert.Equal(t, "new-secret", redisRet.Options.Password)
				assert.Equal(t, test, redisRet.Prefix)
			},
		},
		{
			name: "Redis with full SerializableRedisOptions",
			conf: &retrieverconf.RetrieverConf{
				Kind: retrieverconf.RedisRetriever,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr:                  "redis:6379",
					Password:              "pass",
					DB:                    3,
					MaxRetries:            5,
					DialTimeoutMs:         10000,
					ReadTimeoutMs:         5000,
					PoolSize:              20,
					MinIdleConns:          5,
					ClientName:            "test-client",
					ContextTimeoutEnabled: true,
				},
			},
			wantErr: false,
			validateRetriever: func(t *testing.T, r retriever.Retriever) {
				t.Helper()

				redisRet, ok := r.(*redisretriever.Retriever)
				require.True(t, ok, expectedRedisretrieverRetriever)
				assert.Equal(t, "redis:6379", redisRet.Options.Addr)
				assert.Equal(t, "pass", redisRet.Options.Password)
				assert.Equal(t, 3, redisRet.Options.DB)
				assert.Equal(t, 5, redisRet.Options.MaxRetries)
				assert.Equal(t, 10000*time.Millisecond, redisRet.Options.DialTimeout)
				assert.Equal(t, 5000*time.Millisecond, redisRet.Options.ReadTimeout)
				assert.Equal(t, 20, redisRet.Options.PoolSize)
				assert.Equal(t, 5, redisRet.Options.MinIdleConns)
				assert.Equal(t, "test-client", redisRet.Options.ClientName)
				assert.True(t, redisRet.Options.ContextTimeoutEnabled)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Retriever(tt.conf)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, got)
				assert.IsType(t, &redisretriever.Retriever{}, got)

				if tt.validateRetriever != nil {
					tt.validateRetriever(t, got)
				}
			}
		})
	}
}

func Test_InitRetriever_WithPostgresPools(t *testing.T) {
	t.Parallel()

	pools := postgresqlretriever.NewPoolRegistry()

	tests := []struct {
		wantPools *postgresqlretriever.PoolRegistry
		name      string
		opts      []Option
	}{
		{name: "the registry of the caller is shared", opts: []Option{WithPostgresPools(pools)}, wantPools: pools},
		{name: "without it the retriever owns its pool", opts: nil, wantPools: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Retriever(&retrieverconf.RetrieverConf{
				Kind: retrieverconf.PostgreSQLRetriever, URI: "postgres://localhost/db", Table: fixtures.FlagsDir,
			}, tt.opts...)

			require.NoError(t, err)

			pg, ok := got.(*postgresqlretriever.Retriever)
			require.True(t, ok)
			assert.Same(t, tt.wantPools, pg.Pools)
		})
	}
}

func Test_InitRetriever_UnknownKind(t *testing.T) {
	t.Parallel()

	_, err := Retriever(&retrieverconf.RetrieverConf{Kind: "unknown"})

	require.EqualError(t, err, `invalid retriever: kind "unknown" is not supported`)
}
