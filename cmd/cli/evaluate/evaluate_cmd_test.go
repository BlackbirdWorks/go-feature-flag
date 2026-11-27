package evaluate_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/cli/evaluate"
	"github.com/blackbirdworks/semaphore/cmd/cli/helper"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

const (
	authTokenFlag       = "--auth-token"
	branchFlag          = "--branch"
	checkModeFlag       = "--check-mode"
	columnFlag          = "--column"
	ctxFlag             = "--ctx"
	flagFlag            = "--flag"
	formatFlag          = "--format"
	headerFlag          = "--header"
	kindFlag            = "--kind"
	master              = "master"
	pathFlag            = "--path"
	repositorySlugFlag  = "--repository-slug"
	targetingKeyUser123 = `{"targetingKey": "user-123"}`
	uriFlag             = "--uri"
)

const (
	allFlagResult = "testdata/res/all-flag.json"
	XAPIKeyHeader = "X-API-Key"
)

type cmdEvaluateCase struct {
	wantErr        assert.ErrorAssertionFunc
	name           string
	expectedResult string
	wantStdoutHas  string
	expectedErr    string
	args           []string
}

func runCmdEvaluateTests(t *testing.T, tests []cmdEvaluateCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdOut, stdErr bytes.Buffer

			err := evaluate.Run(t.Context(), tt.args, &helper.Streams{Stdout: &stdOut, Stderr: &stdErr})
			tt.wantErr(t, err)

			if tt.expectedResult != "" {
				expectedContent, readFileErr := os.ReadFile(tt.expectedResult)
				require.NoError(t, readFileErr)
				assert.JSONEq(t, string(expectedContent), stdOut.String())
			}

			if tt.wantStdoutHas != "" {
				assert.Contains(t, stdOut.String(), tt.wantStdoutHas)
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
				kindFlag,
				fixtures.KindFile,
				pathFlag,
				fixtures.FlagGoffYAMLPath,
				flagFlag,
				"non-existent-flag",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr:     assert.Error,
			expectedErr: "evaluating the flag: flag non-existent-flag is not present or disabled",
		},
		{
			name: "should return all flags if no flag is provided",
			args: []string{
				kindFlag,
				fixtures.KindFile,
				pathFlag,
				fixtures.FlagGoffYAMLPath,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr:        assert.NoError,
			expectedResult: allFlagResult,
		},
		{
			name: "should return a single flag if flag name is provided",
			args: []string{
				kindFlag,
				fixtures.KindFile,
				pathFlag,
				fixtures.FlagGoffYAMLPath,
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/single-flag.json",
		},
		{
			name: "should return a single flag if flag name is provided using path flag",
			args: []string{
				kindFlag,
				fixtures.KindFile,
				pathFlag,
				fixtures.FlagGoffYAMLPath,
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/single-flag.json",
		},
		{
			name: "should return an error if configuration file does not exist",
			args: []string{
				"--config",
				"testdata/does-not-exist.yaml",
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr: assert.Error,
		},
		{
			name: "should return an error if context has no targeting key",
			args: []string{
				kindFlag,
				fixtures.KindFile,
				pathFlag,
				fixtures.FlagGoffYAMLPath,
				ctxFlag,
				`{"id": "user-123"}`,
				formatFlag,
				protocol.FormatYAML,
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
				kindFlag,
				"http",
				"--url",
				"http://localhost:8080/config.yaml",
				headerFlag,
				"Content-Type: application/json",
				headerFlag,
				XAPIKeyHeader + ": 123456",
				headerFlag,
				XAPIKeyHeader + ": 654321",
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-http.json",
		},
		{
			name: "should return configuration of github retriever when using check-mode",
			args: []string{
				kindFlag,
				"github",
				repositorySlugFlag,
				fixtures.RepoGoFeatureFlag,
				authTokenFlag,
				"XXX_GH_TOKEN",
				pathFlag,
				fixtures.FlagConfigYAMLPath,
				branchFlag,
				master,
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-github.json",
		},
		{
			name: "should return configuration of gitlab retriever when using check-mode",
			args: []string{
				kindFlag,
				"gitlab",
				"--base-url",
				"https://gitlab.com/api/v4/",
				repositorySlugFlag,
				fixtures.RepoGoFeatureFlag,
				authTokenFlag,
				"XXX_GITLAB_TOKEN",
				branchFlag,
				master,
				pathFlag,
				fixtures.FlagConfigYAMLPath,
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-gitlab.json",
		},
		{
			name: "should return configuration of bitbucket retriever when using check-mode",
			args: []string{
				kindFlag,
				"bitbucket",
				"--base-url",
				"https://bitbucket.com/api/v4/",
				repositorySlugFlag,
				fixtures.RepoGoFeatureFlag,
				authTokenFlag,
				"XXX_BITBUCKET_TOKEN",
				branchFlag,
				master,
				pathFlag,
				fixtures.FlagConfigYAMLPath,
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-bitbucket.json",
		},
		{
			name: "should return configuration of s3 retriever when using check-mode",
			args: []string{
				kindFlag,
				"s3",
				"--bucket",
				"Bucket",
				"--item",
				"valid",
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-s3.json",
		},
		{
			name: "should return configuration of GCS retriever when using check-mode",
			args: []string{
				kindFlag,
				"googleStorage",
				"--bucket",
				"Bucket",
				"--object",
				"flag-config.yaml",
				flagFlag,
				fixtures.FlagKeyTest,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-gcs.json",
		},
		{
			name: "should return configuration of k8s retriever when using check-mode",
			args: []string{
				kindFlag,
				"configmap",
				"--namespace",
				"goff-ns",
				"--config-map",
				"goff-config-map",
				"--key",
				"goff-key",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-k8s.json",
		},
		{
			name: "should return configuration of Mongodb retriever when using check-mode",
			args: []string{
				kindFlag,
				"mongodb",
				uriFlag,
				"mongodb://localhost:27017",
				"--collection",
				"goff-collection",
				"--database",
				"goff-db",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-mongodb.json",
		},
		{
			name: "should return an initialization error if mongodb uri is invalid",
			args: []string{
				kindFlag,
				"mongodb",
				uriFlag,
				"inv4lid",
				"--collection",
				"goff-collection",
				"--database",
				"goff-db",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr: assert.Error,
			expectedErr: "impossible to init retriever: connecting to MongoDB: error parsing uri: " +
				"scheme must be \"mongodb\" or \"mongodb+srv\"",
		},
		{
			name: "should return configuration of Azure Blob Storage retriever when using check-mode",
			args: []string{
				kindFlag,
				"azureBlobStorage",
				"--container",
				"goff-container",
				"--account-name",
				"goff-user",
				"--account-key",
				"goff-key",
				"--object",
				"goff-object",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-azure.json",
		},
		{
			name: "should return configuration of Postgres retriever when using check-mode",
			args: []string{
				kindFlag,
				"postgresql",
				uriFlag,
				"postgresql://localhost:5432",
				"--table",
				"goff-table",
				columnFlag,
				"flag_name: nonexistentcolumn",
				columnFlag,
				"config: config",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
				checkModeFlag,
			},
			wantErr:        assert.NoError,
			expectedResult: "testdata/res/check-postgres.json",
		},
	})
}

func TestCmdEvaluate_Validation(t *testing.T) {
	t.Parallel()

	runCmdEvaluateTests(t, []cmdEvaluateCase{
		{
			name: "should return an initialization error if postgres uri is invalid",
			args: []string{
				kindFlag,
				"postgresql",
				uriFlag,
				"inv4lid",
				"--table",
				"goff-table",
				columnFlag,
				"flag_name: nonexistentcolumn",
				columnFlag,
				"config: config",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr: assert.Error,
			expectedErr: "impossible to init flagset retriever: creating the PostgreSQL pool: cannot parse `inv4lid`: " +
				"failed to parse as keyword/value (invalid keyword/value)",
		},
		{
			name: "should return RetrieverConfError with CLI-friendly message when required property is missing",
			args: []string{
				kindFlag,
				"github",
				pathFlag,
				fixtures.FlagConfigYAMLPath,
				branchFlag,
				master,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr:     assert.Error,
			expectedErr: "invalid retriever: no \"repository-slug\" property found for kind \"github\"",
		},
		{
			name: "should return RetrieverConfError with CLI-friendly message when path is missing for file kind",
			args: []string{
				kindFlag,
				fixtures.KindFile,
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr:     assert.Error,
			expectedErr: "invalid retriever: no \"path\" property found for kind \"file\"",
		},
		{
			name: "should return RetrieverConfError with CLI-friendly message when bucket is missing for s3 kind",
			args: []string{
				kindFlag,
				"s3",
				"--item",
				"config.yaml",
				ctxFlag,
				targetingKeyUser123,
				formatFlag,
				protocol.FormatYAML,
			},
			wantErr:     assert.Error,
			expectedErr: "invalid retriever: no \"bucket\" property found for kind \"s3\"",
		},
		{
			name:          "help exits without error",
			args:          []string{"--help"},
			wantErr:       assert.NoError,
			wantStdoutHas: "Usage: evaluate",
		},
		{
			name: "short -k=file and -f=yaml strip equals signs",
			args: []string{
				"-k=file",
				"-p=" + fixtures.FlagGoffYAMLPath,
				ctxFlag,
				targetingKeyUser123,
				"-f=yaml",
			},
			wantErr:        assert.NoError,
			expectedResult: allFlagResult,
		},
		{
			name:        "unknown flag returns usage error",
			args:        []string{"--nope"},
			wantErr:     assert.Error,
			expectedErr: "usage: unknown flag --nope",
		},
		{
			name: "deprecated --github-token and malformed headers and columns are handled",
			args: []string{
				kindFlag, fixtures.KindFile,
				pathFlag, fixtures.FlagGoffYAMLPath,
				"--github-token", "tok",
				headerFlag, "malformed",
				columnFlag, "malformed",
				ctxFlag, targetingKeyUser123,
				formatFlag, protocol.FormatYAML,
			},
			wantErr:        assert.NoError,
			expectedResult: allFlagResult,
		},
	})

	assert.NotEmpty(t, (&evaluate.Cmd{}).Help())
}
