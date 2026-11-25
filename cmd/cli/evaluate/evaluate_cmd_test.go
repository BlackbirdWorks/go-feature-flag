package evaluate_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
)

const (
	authToken2                  = "--auth-token"
	branch2                     = "--branch"
	checkMode2                  = "--check-mode"
	column2                     = "--column"
	ctx2                        = "--ctx"
	file2                       = "file"
	flag3                       = "--flag"
	format2                     = "--format"
	header2                     = "--header"
	kind2                       = "--kind"
	master2                     = "master"
	path2                       = "--path"
	repositorySlug2             = "--repository-slug"
	targetingKeyUser123         = `{"targetingKey": "user-123"}`
	testFlag                    = "test-flag"
	testdataFlagConfigYaml      = "testdata/flag-config.yaml"
	testdataFlagGoffYaml        = "testdata/flag.goff.yaml"
	thomaspoignantGoFeatureFlag = "thomaspoignant/go-feature-flag"
	uri2                        = "--uri"
	yaml2                       = "yaml"
)

const XAPIKeyHeader = "X-API-Key"

type cmdEvaluateCase struct {
	wantErr        assert.ErrorAssertionFunc
	name           string
	expectedResult string
	expectedErr    string
	args           []string
}

func runCmdEvaluateTests(t *testing.T, tests []cmdEvaluateCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stdOut, err := os.CreateTemp(t.TempDir(), "temp")
			require.NoError(t, err)
			stdErr, err := os.CreateTemp(t.TempDir(), "temp")
			require.NoError(t, err)

			defer func() {
				_ = os.Remove(stdOut.Name())
				_ = os.Remove(stdErr.Name())
			}()

			cmd := evaluate.NewEvaluateCmd()
			cmd.SetErr(stdErr)
			cmd.SetOut(stdOut)
			cmd.SetArgs(tt.args)
			err = cmd.Execute()
			tt.wantErr(t, err)

			if tt.expectedResult != "" {
				expectedContent, readFileErr := os.ReadFile(tt.expectedResult)
				require.NoError(t, readFileErr)
				gotContent, readFileErr := os.ReadFile(stdOut.Name())
				require.NoError(t, readFileErr)
				assert.JSONEq(t, string(expectedContent), string(gotContent))
			}

			if tt.expectedErr != "" {
				assert.EqualError(t, err, tt.expectedErr)
			}
		})
	}
}

func TestCmdEvaluate(t *testing.T) {
	t.Parallel()

	runCmdEvaluateTests(t, []cmdEvaluateCase{
		{
			name: "should return an error if flag does not exists",
			args: []string{
				kind2,
				file2,
				path2,
				testdataFlagGoffYaml,
				flag3,
				"non-existent-flag",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr:     assert.Error,
			expectedErr: "evaluating the flag: flag non-existent-flag is not present or disabled",
		},
		{
			name: "should return all flags if no flag is provided",
			args: []string{
				kind2,
				file2,
				path2,
				testdataFlagGoffYaml,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/all-flag.json",
		},
		{
			name: "should return a single flag if flag name is provided",
			args: []string{
				kind2,
				file2,
				path2,
				testdataFlagGoffYaml,
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/single-flag.json",
		},
		{
			name: "should return a single flag if flag name is provided using path flag",
			args: []string{
				kind2,
				file2,
				path2,
				testdataFlagGoffYaml,
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/single-flag.json",
		},
		{
			name: "should return an error if configuration file does not exist",
			args: []string{
				"--config",
				"testdata/does-not-exist.yaml",
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr: assert.Error,
		},
		{
			name: "should return an error if context has no targeting key",
			args: []string{
				kind2,
				file2,
				path2,
				testdataFlagGoffYaml,
				ctx2,
				`{"id": "user-123"}`,
				format2,
				yaml2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/all-flag-no-targeting.json",
		},
	})
}

func TestCmdEvaluate_CheckModeAndValidation(t *testing.T) {
	t.Parallel()

	runCmdEvaluateTests(t, []cmdEvaluateCase{
		{
			name: "should return configuration of http retriever with headers set properly when using check-mode",
			args: []string{
				kind2,
				"http",
				"--url",
				"http://localhost:8080/config.yaml",
				header2,
				"Content-Type: application/json",
				header2,
				XAPIKeyHeader + ": 123456",
				header2,
				XAPIKeyHeader + ": 654321",
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-http.json",
		},
		{
			name: "should return configuration of github retriever when using check-mode",
			args: []string{
				kind2,
				"github",
				repositorySlug2,
				thomaspoignantGoFeatureFlag,
				authToken2,
				"XXX_GH_TOKEN",
				path2,
				testdataFlagConfigYaml,
				branch2,
				master2,
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-github.json",
		},
		{
			name: "should return configuration of gitlab retriever when using check-mode",
			args: []string{
				kind2,
				"gitlab",
				"--base-url",
				"https://gitlab.com/api/v4/",
				repositorySlug2,
				thomaspoignantGoFeatureFlag,
				authToken2,
				"XXX_GITLAB_TOKEN",
				branch2,
				master2,
				path2,
				testdataFlagConfigYaml,
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-gitlab.json",
		},
		{
			name: "should return configuration of bitbucket retriever when using check-mode",
			args: []string{
				kind2,
				"bitbucket",
				"--base-url",
				"https://bitbucket.com/api/v4/",
				repositorySlug2,
				thomaspoignantGoFeatureFlag,
				authToken2,
				"XXX_BITBUCKET_TOKEN",
				branch2,
				master2,
				path2,
				testdataFlagConfigYaml,
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-bitbucket.json",
		},
		{
			name: "should return configuration of s3 retriever when using check-mode",
			args: []string{
				kind2,
				"s3",
				"--bucket",
				"Bucket",
				"--item",
				"valid",
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-s3.json",
		},
		{
			name: "should return configuration of GCS retriever when using check-mode",
			args: []string{
				kind2,
				"googleStorage",
				"--bucket",
				"Bucket",
				"--object",
				"flag-config.yaml",
				flag3,
				testFlag,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-gcs.json",
		},
		{
			name: "should return configuration of k8s retriever when using check-mode",
			args: []string{
				kind2,
				"configmap",
				"--namespace",
				"goff-ns",
				"--config-map",
				"goff-config-map",
				"--key",
				"goff-key",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-k8s.json",
		},
		{
			name: "should return configuration of Mongodb retriever when using check-mode",
			args: []string{
				kind2,
				"mongodb",
				uri2,
				"mongodb://localhost:27017",
				"--collection",
				"goff-collection",
				"--database",
				"goff-db",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-mongodb.json",
		},
		{
			name: "should return an initialization error if mongodb uri is invalid",
			args: []string{
				kind2,
				"mongodb",
				uri2,
				"inv4lid",
				"--collection",
				"goff-collection",
				"--database",
				"goff-db",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr: assert.Error,
			expectedErr: "impossible to init retriever: connecting to MongoDB: error parsing uri: " +
				"scheme must be \"mongodb\" or \"mongodb+srv\"",
		},
		{
			name: "should return configuration of Azure Blob Storage retriever when using check-mode",
			args: []string{
				kind2,
				"azureBlobStorage",
				"--container",
				"goff-container",
				"--account-name",
				"goff-user",
				"--account-key",
				"goff-key",
				"--object",
				"goff-object",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-azure.json",
		},
		{
			name: "should return configuration of Postgres retriever when using check-mode",
			args: []string{
				kind2,
				"postgresql",
				uri2,
				"postgresql://localhost:5432",
				"--table",
				"goff-table",
				column2,
				"flag_name: nonexistentcolumn",
				column2,
				"config: config",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
				checkMode2,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-postgres.json",
		},
		{
			name: "should return an initialization error if postgres uri is invalid",
			args: []string{
				kind2,
				"postgresql",
				uri2,
				"inv4lid",
				"--table",
				"goff-table",
				column2,
				"flag_name: nonexistentcolumn",
				column2,
				"config: config",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr: assert.Error,
			expectedErr: "impossible to init flagset retriever: creating the PostgreSQL pool: cannot parse `inv4lid`: " +
				"failed to parse as keyword/value (invalid keyword/value)",
		},
		{
			name: "should return RetrieverConfError with CLI-friendly message when required property is missing",
			args: []string{
				kind2,
				"github",
				path2,
				testdataFlagConfigYaml,
				branch2,
				master2,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr:     assert.Error,
			expectedErr: "invalid retriever: no \"repository-slug\" property found for kind \"github\"",
		},
		{
			name: "should return RetrieverConfError with CLI-friendly message when path is missing for file kind",
			args: []string{
				kind2,
				file2,
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr:     assert.Error,
			expectedErr: "invalid retriever: no \"path\" property found for kind \"file\"",
		},
		{
			name: "should return RetrieverConfError with CLI-friendly message when bucket is missing for s3 kind",
			args: []string{
				kind2,
				"s3",
				"--item",
				"config.yaml",
				ctx2,
				targetingKeyUser123,
				format2,
				yaml2,
			},
			wantErr:     assert.Error,
			expectedErr: "invalid retriever: no \"bucket\" property found for kind \"s3\"",
		},
	})
}
