package retrieverconf_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
)

const (
	azureBlobStorage      = "azureBlobStorage"
	bEGINRSAPRIVATEKEY    = "-----BEGIN RSA PRIVATE KEY-----\n..."
	configmap             = "configmap"
	eby8VdM02             = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq"
	flags                 = "flags/"
	flagsConfigYaml       = "flags/config.yaml"
	github                = "github"
	googleStorage         = "googleStorage"
	http2                 = "http"
	mongodb               = "mongodb"
	myorgMyrepo           = "myorg/myrepo"
	noBucketPropertyFound = "no \"bucket\" property found for kind \"s3\""
	postgresql            = "postgresql"
	redis2                = "redis"
	testBucket            = "testBucket"
	testYaml              = "test.yaml"
	xXX                   = "XXX"
	xxx                   = "xxx"
)

type retrieverConfValidCase struct {
	name     string
	errValue string
	fields   retrieverconf.RetrieverConf
	wantErr  bool
}

// invalidPrefix starts the errors of the missing properties.
const invalidPrefix = "validating the retriever configuration: invalid retriever: "

func runRetrieverConfValidTests(t *testing.T, tests []retrieverConfValidCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := tt.fields
			err := c.IsValid()
			assert.Equal(t, tt.wantErr, err != nil)

			if tt.wantErr {
				assert.Equal(t, tt.errValue, err.Error())
			}
		})
	}
}

func TestRetrieverConf_IsValid(t *testing.T) {
	t.Parallel()

	runRetrieverConfValidTests(t, []retrieverConfValidCase{
		{
			name:     "no fields",
			fields:   retrieverconf.RetrieverConf{},
			wantErr:  true,
			errValue: "invalid retriever: kind \"\" is not supported",
		},
		{
			name: "invalid kind",
			fields: retrieverconf.RetrieverConf{
				Kind: "invalid",
			},
			wantErr:  true,
			errValue: "invalid retriever: kind \"invalid\" is not supported",
		},
		{
			name: "kind GitHubRetriever without repo slug",
			fields: retrieverconf.RetrieverConf{
				Kind: github,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"repositorySlug\" property found for kind \"github\"",
		},
		{
			name: "kind GitlabRetriever, with URL but without path",
			fields: retrieverconf.RetrieverConf{
				Kind: "gitlab",
				URL:  xXX,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"repositorySlug\" property found for kind \"gitlab\"",
		},
		{
			name: "kind GitlabRetriever, with URL but without path",
			fields: retrieverconf.RetrieverConf{
				Kind:           "gitlab",
				RepositorySlug: "aaa/bbb",
				URL:            xXX,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"path\" property found for kind \"gitlab\"",
		},
		{
			name: "kind BitbucketRetriever without repo slug",
			fields: retrieverconf.RetrieverConf{
				Kind: "bitbucket",
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"repositorySlug\" property found for kind \"bitbucket\"",
		},
		{
			name: "kind S3Retriever without bucket",
			fields: retrieverconf.RetrieverConf{
				Kind: "s3",
			},
			wantErr:  true,
			errValue: invalidPrefix + noBucketPropertyFound,
		},
		{
			name: "kind S3Retriever with item and prefix",
			fields: retrieverconf.RetrieverConf{
				Kind:   "s3",
				Bucket: testBucket,
				Item:   testYaml,
				Prefix: flags,
			},
			wantErr:  true,
			errValue: "invalid retriever: item and prefix are mutually exclusive for kind \"s3\"",
		},
		{
			name: "kind s3 with item and pattern",
			fields: retrieverconf.RetrieverConf{
				Kind: "s3", Bucket: testBucket, Item: testYaml, Pattern: "*.yaml",
			},
			wantErr:  true,
			errValue: "invalid retriever: pattern and recursive can only be used without item for kind \"s3\"",
		},
		{
			name: "kind s3 with item and recursive",
			fields: retrieverconf.RetrieverConf{
				Kind: "s3", Bucket: testBucket, Item: testYaml, Recursive: true,
			},
			wantErr:  true,
			errValue: "invalid retriever: pattern and recursive can only be used without item for kind \"s3\"",
		},
		{
			name: "kind s3 with invalid pattern",
			fields: retrieverconf.RetrieverConf{
				Kind: "s3", Bucket: testBucket, Prefix: flags, Pattern: "[a-",
			},
			wantErr:  true,
			errValue: "invalid retriever: invalid pattern \"[a-\": syntax error in pattern for kind \"s3\"",
		},
		{
			name: "kind s3 with pattern containing a slash",
			fields: retrieverconf.RetrieverConf{
				Kind: "s3", Bucket: testBucket, Prefix: flags, Pattern: "sub/*.yaml",
			},
			wantErr: true,
			errValue: "invalid retriever: invalid pattern \"sub/*.yaml\": it is matched against the file name only and can" +
				"'t contain \"/\" for kind \"s3\"",
		},
		{
			name: "kind s3 with prefix and no bucket",
			fields: retrieverconf.RetrieverConf{
				Kind:   "s3",
				Prefix: flags,
			},
			wantErr:  true,
			errValue: invalidPrefix + noBucketPropertyFound,
		},
		{
			name: "kind HTTPRetriever without URL",
			fields: retrieverconf.RetrieverConf{
				Kind: http2,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"url\" property found for kind \"http\"",
		},
		{
			name: "kind HTTPRetriever with client certificate but without client key",
			fields: retrieverconf.RetrieverConf{
				Kind:               http2,
				URL:                "https://gofeatureflag.org/my-flag-test.yaml",
				HTTPClientCertPath: "client.crt",
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"clientKeyPath\" property found for kind \"http\"",
		},
		{
			name: "kind HTTPRetriever with client key but without client certificate",
			fields: retrieverconf.RetrieverConf{
				Kind:              http2,
				URL:               "https://gofeatureflag.org/my-flag-test.yaml",
				HTTPClientKeyPath: "client.key",
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"clientCertPath\" property found for kind \"http\"",
		},
		{
			name: "kind GCP without Object",
			fields: retrieverconf.RetrieverConf{
				Kind: googleStorage,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"object\" property found for kind \"googleStorage\"",
		},
		{
			name: "kind GitHubRetriever without path",
			fields: retrieverconf.RetrieverConf{
				Kind:           github,
				RepositorySlug: "thomaspoignant/go-feature-flag",
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"path\" property found for kind \"github\"",
		},
		{
			name: "kind file without path",
			fields: retrieverconf.RetrieverConf{
				Kind: "file",
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"path\" property found for kind \"file\"",
		},
		{
			name: "kind s3 without bucket",
			fields: retrieverconf.RetrieverConf{
				Kind: "s3",
				Item: testYaml,
			},
			wantErr:  true,
			errValue: invalidPrefix + noBucketPropertyFound,
		},
		{
			name: "kind google storage without bucket",
			fields: retrieverconf.RetrieverConf{
				Kind:   googleStorage,
				Object: testYaml,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"bucket\" property found for kind \"googleStorage\"",
		},
		{
			name: "kind azureBlobStorage without object",
			fields: retrieverconf.RetrieverConf{
				Kind:        azureBlobStorage,
				Container:   "testcontainer",
				AccountName: "devstoreaccount1",
				AccountKey:  eby8VdM02,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"object\" property found for kind \"azureBlobStorage\"",
		},
		{
			name: "kind azureBlobStorage without accountName",
			fields: retrieverconf.RetrieverConf{
				Kind:       azureBlobStorage,
				Container:  "testcontainer",
				Object:     "flag-config.yaml",
				AccountKey: eby8VdM02,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"accountName\" property found for kind \"azureBlobStorage\"",
		},
		{
			name: "kind azureBlobStorage without container",
			fields: retrieverconf.RetrieverConf{
				Kind:        azureBlobStorage,
				Object:      "flag-config.yaml",
				AccountName: "devstoreaccount1",
				AccountKey:  eby8VdM02,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"container\" property found for kind \"azureBlobStorage\"",
		},
	})
}

func TestRetrieverConf_IsValid_CloudAndHTTP(t *testing.T) {
	t.Parallel()

	runRetrieverConfValidTests(t, []retrieverConfValidCase{
		{
			name: "valid s3",
			fields: retrieverconf.RetrieverConf{
				Kind:   "s3",
				Item:   testYaml,
				Bucket: testBucket,
			},
		},
		{
			name: "valid s3 without item nor prefix (bucket root)",
			fields: retrieverconf.RetrieverConf{
				Kind:   "s3",
				Bucket: testBucket,
			},
		},
		{
			name: "valid s3 with prefix",
			fields: retrieverconf.RetrieverConf{
				Kind:      "s3",
				Prefix:    flags,
				Pattern:   "*.yaml",
				Recursive: true,
				Bucket:    testBucket,
			},
		},
		{
			name: "valid s3 with root prefix",
			fields: retrieverconf.RetrieverConf{
				Kind:      "s3",
				Prefix:    "/",
				Recursive: true,
				Bucket:    testBucket,
			},
		},
		{
			name: "valid googleStorage",
			fields: retrieverconf.RetrieverConf{
				Kind:   googleStorage,
				Object: testYaml,
				Bucket: testBucket,
			},
		},
		{
			name: "valid github",
			fields: retrieverconf.RetrieverConf{
				Kind:           github,
				RepositorySlug: "thomaspoignant/go-feature-flag",
				Branch:         "main",
				Path:           "testdata/config.yaml",
				GithubToken:    xXX,
				Timeout:        5000,
			},
		},
		{
			name: "valid file",
			fields: retrieverconf.RetrieverConf{
				Kind: "file",
				Path: "testdata/config.yaml",
			},
		},
		{
			name: "valid http",
			fields: retrieverconf.RetrieverConf{
				Kind:       http2,
				URL:        "http://perdu.com/flags",
				HTTPMethod: http.MethodGet,
				HTTPBody:   `{"yo"": "yo"}`,
				HTTPHeaders: map[string][]string{
					"Test": {"Val1"},
				},
				Timeout: 5000,
			},
		},
		{
			name: "kind k8s configmap without namespace",
			fields: retrieverconf.RetrieverConf{
				Kind:      configmap,
				Namespace: "",
				Key:       xxx,
				ConfigMap: xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"namespace\" property found for kind \"configmap\"",
		},
		{
			name: "kind k8s configmap without key",
			fields: retrieverconf.RetrieverConf{
				Kind:      configmap,
				Namespace: xxx,
				ConfigMap: xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"key\" property found for kind \"configmap\"",
		},
		{
			name: "kind k8s configmap without ConfigMap",
			fields: retrieverconf.RetrieverConf{
				Kind:      configmap,
				Namespace: xxx,
				Key:       xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"configmap\" property found for kind \"configmap\"",
		},
		{
			name: "kind k8s valid",
			fields: retrieverconf.RetrieverConf{
				Kind:      configmap,
				Namespace: xxx,
				Key:       xxx,
				ConfigMap: xxx,
			},
		},
		{
			name: "kind mongoDB without URI",
			fields: retrieverconf.RetrieverConf{
				Kind:       mongodb,
				URI:        "",
				Collection: xxx,
				Database:   xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"uri\" property found for kind \"mongodb\"",
		},
		{
			name: "kind redis without options (old RedisOptions)",
			fields: retrieverconf.RetrieverConf{
				Kind:        redis2,
				RedisPrefix: xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"redisOptions\" property found for kind \"redis\"",
		},
		{
			name: "kind redis with RedisOptions",
			fields: retrieverconf.RetrieverConf{
				Kind: redis2,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr: localhost6379,
				},
				RedisPrefix: xxx,
			},
			wantErr: false,
		},
		{
			name: "kind redis with SerializableRedisOptions",
			fields: retrieverconf.RetrieverConf{
				Kind: redis2,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr: localhost6379,
				},
				RedisPrefix: xxx,
			},
			wantErr: false,
		},
		{
			name: "kind redis with RedisOptions",
			fields: retrieverconf.RetrieverConf{
				Kind: redis2,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr: "new:6379",
				},
				RedisPrefix: xxx,
			},
			wantErr: false,
		},
		{
			name: "kind redis with RedisOptions but empty Addr",
			fields: retrieverconf.RetrieverConf{
				Kind: redis2,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr: "",
				},
				RedisPrefix: xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"redisOptions.addr\" property found for kind \"redis\"",
		},
		{
			name: "kind redis with RedisOptions but empty Addr (2)",
			fields: retrieverconf.RetrieverConf{
				Kind: redis2,
				RedisOptions: &retrieverconf.SerializableRedisOptions{
					Addr: "",
				},
				RedisPrefix: xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"redisOptions.addr\" property found for kind \"redis\"",
		},
		{
			name: "kind mongoDB without Collection",
			fields: retrieverconf.RetrieverConf{
				Kind:       mongodb,
				URI:        xxx,
				Collection: "",
				Database:   xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"collection\" property found for kind \"mongodb\"",
		},
		{
			name: "kind mongoDB without database",
			fields: retrieverconf.RetrieverConf{
				Kind:       mongodb,
				URI:        xxx,
				Collection: xxx,
				Database:   "",
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"database\" property found for kind \"mongodb\"",
		},
		{
			name: "kind mongoDB valid",
			fields: retrieverconf.RetrieverConf{
				Kind:       mongodb,
				URI:        xxx,
				Collection: xxx,
				Database:   xxx,
			},
		},
		{
			name: "kind postgresql valid",
			fields: retrieverconf.RetrieverConf{
				Kind:  postgresql,
				URI:   xxx,
				Table: xxx,
			},
		},
		{
			name: "kind postgresql invalid without URI",
			fields: retrieverconf.RetrieverConf{
				Kind:  postgresql,
				Table: xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"uri\" property found for kind \"postgresql\"",
		},
		{
			name: "kind postgresql invalid without Table",
			fields: retrieverconf.RetrieverConf{
				Kind: postgresql,
				URI:  xxx,
			},
			wantErr:  true,
			errValue: invalidPrefix + "no \"table\" property found for kind \"postgresql\"",
		},
	})
}

func TestRetrieverConf_validateGitHubAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		errValue string
		fields   retrieverconf.RetrieverConf
		wantErr  bool
	}{
		{
			name: "valid: App auth with inline PEM key",
			fields: retrieverconf.RetrieverConf{
				Kind:                 github,
				RepositorySlug:       myorgMyrepo,
				Path:                 flagsConfigYaml,
				GitHubAppID:          12345,
				GitHubInstallationID: 67890,
				GitHubAppPrivateKey:  "-----BEGIN RSA PRIVATE KEY-----\n...\n-----END RSA PRIVATE KEY-----",
			},
			wantErr: false,
		},
		{
			name: "valid: App auth with key file",
			fields: retrieverconf.RetrieverConf{
				Kind:                    github,
				RepositorySlug:          myorgMyrepo,
				Path:                    flagsConfigYaml,
				GitHubAppID:             12345,
				GitHubInstallationID:    67890,
				GitHubAppPrivateKeyFile: "/var/secrets/app.pem",
			},
			wantErr: false,
		},
		{
			name: "valid: PAT auth (no App fields)",
			fields: retrieverconf.RetrieverConf{
				Kind:           github,
				RepositorySlug: myorgMyrepo,
				Path:           flagsConfigYaml,
				AuthToken:      "ghp_xxxx",
			},
			wantErr: false,
		},
		{
			name: "valid: no auth",
			fields: retrieverconf.RetrieverConf{
				Kind:           github,
				RepositorySlug: myorgMyrepo,
				Path:           flagsConfigYaml,
			},
			wantErr: false,
		},
		{
			name: "invalid: token and App fields together",
			fields: retrieverconf.RetrieverConf{
				Kind:                 github,
				RepositorySlug:       myorgMyrepo,
				Path:                 flagsConfigYaml,
				AuthToken:            "ghp_xxxx",
				GitHubAppID:          12345,
				GitHubInstallationID: 67890,
				GitHubAppPrivateKey:  bEGINRSAPRIVATEKEY,
			},
			wantErr: true,
			errValue: `invalid retriever: token and GitHub App fields (githubAppId, githubInstallationId, githubAppPrivate` +
				`Key / githubAppPrivateKeyFile) are mutually exclusive for kind "github"`,
		},
		{
			name: "invalid: App fields missing AppID",
			fields: retrieverconf.RetrieverConf{
				Kind:                 github,
				RepositorySlug:       myorgMyrepo,
				Path:                 flagsConfigYaml,
				GitHubInstallationID: 67890,
				GitHubAppPrivateKey:  bEGINRSAPRIVATEKEY,
			},
			wantErr:  true,
			errValue: invalidPrefix + `no "githubAppId" property found for kind "github"`,
		},
		{
			name: "invalid: App fields missing InstallationID",
			fields: retrieverconf.RetrieverConf{
				Kind:                github,
				RepositorySlug:      myorgMyrepo,
				Path:                flagsConfigYaml,
				GitHubAppID:         12345,
				GitHubAppPrivateKey: bEGINRSAPRIVATEKEY,
			},
			wantErr:  true,
			errValue: invalidPrefix + `no "githubInstallationId" property found for kind "github"`,
		},
		{
			name: "invalid: App fields missing private key",
			fields: retrieverconf.RetrieverConf{
				Kind:                 github,
				RepositorySlug:       myorgMyrepo,
				Path:                 flagsConfigYaml,
				GitHubAppID:          12345,
				GitHubInstallationID: 67890,
			},
			wantErr: true,
			errValue: invalidPrefix + `no ` +
				`"githubAppPrivateKey or githubAppPrivateKeyFile" property found for kind "github"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := tt.fields
			err := c.IsValid()
			assert.Equal(t, tt.wantErr, err != nil)

			if tt.wantErr {
				assert.Equal(t, tt.errValue, err.Error())
			}
		})
	}
}
