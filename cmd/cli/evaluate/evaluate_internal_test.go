package evaluate

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/api/option"

	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	retrieverinit "github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf/init"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/bitbucketretriever"
	"github.com/blackbirdworks/semaphore/retriever/gcstorageretriever"
	"github.com/blackbirdworks/semaphore/retriever/githubretriever"
	"github.com/blackbirdworks/semaphore/retriever/gitlabretriever"
	"github.com/blackbirdworks/semaphore/retriever/httpretriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
	"github.com/blackbirdworks/semaphore/testhelpers"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/mock"
)

const (
	defaultUpper        = "DEFAULT"
	httpsJiraXxxGOFF    = "https://jira.xxx/GOFF-01"
	simpleFlag          = "simple-flag"
	targetingKeyUser123 = `{"targetingKey": "user-123"}`
)

// evaluateTestCase describes a single Test_Evaluate scenario.
type evaluateTestCase struct {
	initEvaluate   func() (evaluate, error)
	wantErr        assert.ErrorAssertionFunc
	expectedResult map[string]model.RawVarResult
	name           string
	expectedErr    string
}

// buildEvaluate initializes a retriever from the given configuration and wraps
// it into an evaluate ready to be executed. The optional customize callback is
// invoked on the freshly initialized retriever (e.g. to inject a mocked client)
// before the evaluate is returned.
func buildEvaluate(
	conf *retrieverconf.RetrieverConf,
	flag string,
	evaluationCtx string,
	customize func(r retriever.Retriever),
) (evaluate, error) {
	built, err := retrieverinit.Retriever(conf)
	if err != nil {
		return evaluate{}, err
	}

	r := built.Retriever

	if customize != nil {
		customize(r)
	}

	return evaluate{
		retriever:     r,
		fileFormat:    "yaml",
		flag:          flag,
		evaluationCtx: evaluationCtx,
	}, nil
}

// assertEvaluate runs a single evaluateTestCase and verifies its outcome.
func assertEvaluate(t *testing.T, tt evaluateTestCase) {
	t.Helper()

	eval, err := tt.initEvaluate()
	if err != nil {
		tt.wantErr(t, err)

		return
	}

	got, err := eval.Evaluate()
	tt.wantErr(t, err)

	if tt.expectedErr != "" {
		// Contains (not Equal) so the assertion stays valid across platforms: the
		// underlying OS error text differs (e.g. "no such file or directory" on
		// Unix vs "The system cannot find the file specified." on Windows).
		assert.Contains(t, err.Error(), tt.expectedErr)
	}

	if tt.expectedResult != nil {
		assert.Equal(t, tt.expectedResult, got)
	}
}

func runEvaluateTests(t *testing.T, tests []evaluateTestCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assertEvaluate(t, tt)
		})
	}
}

func Test_Evaluate(t *testing.T) {
	t.Parallel()

	runEvaluateTests(t, []evaluateTestCase{
		{
			name: "Should error is config file does not exist",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind: fixtures.KindFile,
					Path: "testdata/invalid.yaml",
				}, fixtures.FlagKeyTest, targetingKeyUser123, nil)
			},
			wantErr: assert.Error,
			// OS-specific suffix ("no such file or directory" / "The system cannot
			// find the file specified.") is intentionally omitted; asserted via Contains.
			expectedErr: "initializing the evaluation client: impossible to initialize the retrievers, please check your " +
				"configuration: impossible to retrieve the flags, please check your configuration: reading the file: " +
				"open testdata/invalid.yaml:",
		},
		{
			name: "Should error if no evaluation context provided with flag containing percentage rules",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: fixtures.KindFile, Path: fixtures.FlagGoffYAMLPath},
					simpleFlag, "", nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				simpleFlag: {
					TrackEvents:   true,
					VariationType: "B",
					Failed:        false,
					Version:       "",
					Reason:        fixtures.VariationStatic,
					Value:         true,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink:        httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should perform evaluation if no evaluation context and compatible flag",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: fixtures.KindFile, Path: fixtures.FlagGoffYAMLPath},
					simpleFlag, "", nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				simpleFlag: {
					TrackEvents:   true,
					VariationType: "B",
					Failed:        false,
					Version:       "",
					Reason:        fixtures.VariationStatic,
					Value:         true,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink:        httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should error if evaluation context provided has no targeting key",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: fixtures.KindFile, Path: fixtures.FlagGoffYAMLPath},
					fixtures.FlagKeyTest, `{"id": "user-123"}`, nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: "SdkDefault",
					Failed:        true,
					Version:       "",
					Reason:        "ERROR",
					ErrorCode:     "TARGETING_KEY_MISSING",
					ErrorDetails:  "Error: Empty targeting key",
					Value:         nil,
					Cacheable:     false,
					Metadata: map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink:        httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should evaluate a single flag if flag name is provided",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: fixtures.KindFile, Path: fixtures.FlagGoffYAMLPath},
					fixtures.FlagKeyTest, targetingKeyUser123, nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink:        httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should evaluate all flags if flag name is not provided",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: fixtures.KindFile, Path: fixtures.FlagGoffYAMLPath},
					"", targetingKeyUser123, nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink:        httpsJiraXxxGOFF,
					},
				},
				"test-flag2": {
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
				},
				simpleFlag: {
					TrackEvents:   true,
					VariationType: "B",
					Failed:        false,
					Version:       "",
					Reason:        fixtures.VariationStatic,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         true,
					Cacheable:     true,
					Metadata: map[string]any{
						fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink:        httpsJiraXxxGOFF,
					},
				},
			},
		},
	})
}

func Test_Evaluate_RemoteRetrievers(t *testing.T) {
	t.Parallel()

	runEvaluateTests(t, []evaluateTestCase{
		{
			name: "Should evaluate a flag from a github repository",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:           "github",
					RepositorySlug: fixtures.RepoGoFeatureFlag,
					GithubToken:    "XXX_GH_TOKEN", //nolint:staticcheck // SA1019: the test covers the deprecated setting
					Path:           fixtures.FlagConfigYAMLPath,
				}, fixtures.FlagKeyTest, targetingKeyUser123, func(r retriever.Retriever) {
					gitHubRetriever, ok := r.(*githubretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *githubretriever.Retriever")
					gitHubRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.FalseVar,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata:      nil,
				},
			},
		},
		{
			name: "Should evaluate a flag from a gitlab repository",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:           "gitlab",
					BaseURL:        "https://gitlab.com/api/v4/",
					RepositorySlug: fixtures.RepoGoFeatureFlag,
					AuthToken:      "XXX",
					Path:           fixtures.FlagConfigYAMLPath,
				}, fixtures.FlagKeyTest, targetingKeyUser123, func(r retriever.Retriever) {
					gitLabRetriever, ok := r.(*gitlabretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *gitlabretriever.Retriever")
					gitLabRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.FalseVar,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata:      nil,
				},
			},
		},
		{
			name: "Should evaluate a flag from a bitbucket repository",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:           "bitbucket",
					BaseURL:        "https://bitbucket.com/api/v4/",
					RepositorySlug: fixtures.RepoGoFeatureFlag,
					AuthToken:      "XXX",
					Path:           fixtures.FlagConfigYAMLPath,
				}, fixtures.FlagKeyTest, targetingKeyUser123, func(r retriever.Retriever) {
					bitBucketRetriever, ok := r.(*bitbucketretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *bitbucketretriever.Retriever")
					bitBucketRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.FalseVar,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata:      nil,
				},
			},
		},
		{
			name: "Should evaluate a flag from a S3 repository",
			initEvaluate: func() (evaluate, error) {
				downloader := &testhelpers.S3ManagerV2Mock{
					TestDataLocation: "./testdata",
				}

				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:   "s3",
					Bucket: "Bucket",
					Item:   "valid",
				}, fixtures.FlagKeyTest, targetingKeyUser123, func(r retriever.Retriever) {
					s3Retriever, ok := r.(*s3retrieverv2.Retriever)
					assert.True(t, ok, "failed to assert retriever to *s3retrieverv2.Retriever")
					s3Retriever.SetDownloader(downloader)
					_ = s3Retriever.Init(context.Background(), slog.New(slog.DiscardHandler))
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink: httpsJiraXxxGOFF},
				},
			},
		},
		{
			name: "Should evaluate a flag from a HTTP endpoint",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:        "http",
					URL:         "http://localhost.example/file",
					HTTPMethod:  http.MethodGet,
					HTTPBody:    "",
					HTTPHeaders: nil,
				}, fixtures.FlagKeyTest, targetingKeyUser123, func(r retriever.Retriever) {
					httpRetriever, ok := r.(*httpretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *httpretriever.Retriever")
					httpRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.FalseVar,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata:      nil,
				},
			},
		},
		{
			name: "Should evaluate a flag from a GCS",
			initEvaluate: func() (evaluate, error) {
				mockedStorage := testhelpers.NewMockedGCS(t)
				mockedStorage.WithFiles(t, "flags", map[string]string{fixtures.FlagConfigYAMLPath: "flag-config.yaml"})

				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:   "googleStorage",
					Bucket: "flags",
					Object: "flag-config.yaml",
				}, fixtures.FlagKeyTest, targetingKeyUser123, func(r retriever.Retriever) {
					gcsRetriever, ok := r.(*gcstorageretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *gcstorageretriever.Retriever")
					gcsRetriever.SetOptions([]option.ClientOption{
						option.WithoutAuthentication(),
						option.WithHTTPClient(mockedStorage.Server.HTTPClient()),
					})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				fixtures.FlagKeyTest: {
					TrackEvents:   true,
					VariationType: fixtures.VariationDefault,
					Failed:        false,
					Version:       "",
					Reason:        defaultUpper,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{fixtures.DescriptionField: fixtures.SimpleFeatureFlagDescription,
						fixtures.IssueLink: httpsJiraXxxGOFF},
				},
			},
		},
	})
}
