package evaluate

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/api/option"

	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	retrieverInit "github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf/init"
	"github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/bitbucketretriever"
	"github.com/blackbirdworks/semaphore/retriever/gcstorageretriever"
	"github.com/blackbirdworks/semaphore/retriever/githubretriever"
	"github.com/blackbirdworks/semaphore/retriever/gitlabretriever"
	"github.com/blackbirdworks/semaphore/retriever/httpretriever"
	"github.com/blackbirdworks/semaphore/retriever/s3retrieverv2"
	"github.com/blackbirdworks/semaphore/testutils"
	"github.com/blackbirdworks/semaphore/testutils/mock"
)

const (
	dEFAULT                     = "DEFAULT"
	description2                = "description"
	falseVar                    = "false_var"
	file2                       = "file"
	variationDefault            = "Default"
	httpsJiraXxxGOFF            = "https://jira.xxx/GOFF-01"
	issueLink                   = "issue-link"
	sTATIC                      = "STATIC"
	simpleFlag                  = "simple-flag"
	targetingKeyUser123         = `{"targetingKey": "user-123"}`
	testFlag                    = "test-flag"
	testdataFlagConfigYaml      = "testdata/flag-config.yaml"
	testdataFlagGoffYaml        = "testdata/flag.goff.yaml"
	thisIsASimple               = "this is a simple feature flag"
	thomaspoignantGoFeatureFlag = "thomaspoignant/go-feature-flag"
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
	r, err := retrieverInit.Retriever(conf)
	if err != nil {
		return evaluate{}, err
	}

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
					Kind: file2,
					Path: "testdata/invalid.yaml",
				}, testFlag, targetingKeyUser123, nil)
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
					&retrieverconf.RetrieverConf{Kind: file2, Path: testdataFlagGoffYaml},
					simpleFlag, "", nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				simpleFlag: {
					TrackEvents:   true,
					VariationType: "B",
					Failed:        false,
					Version:       "",
					Reason:        sTATIC,
					Value:         true,
					Cacheable:     true,
					Metadata: map[string]any{
						description2: thisIsASimple,
						issueLink:    httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should perform evaluation if no evaluation context and compatible flag",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: file2, Path: testdataFlagGoffYaml},
					simpleFlag, "", nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				simpleFlag: {
					TrackEvents:   true,
					VariationType: "B",
					Failed:        false,
					Version:       "",
					Reason:        sTATIC,
					Value:         true,
					Cacheable:     true,
					Metadata: map[string]any{
						description2: thisIsASimple,
						issueLink:    httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should error if evaluation context provided has no targeting key",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: file2, Path: testdataFlagGoffYaml},
					testFlag, `{"id": "user-123"}`, nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
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
						description2: thisIsASimple,
						issueLink:    httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should evaluate a single flag if flag name is provided",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: file2, Path: testdataFlagGoffYaml},
					testFlag, targetingKeyUser123, nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
					TrackEvents:   true,
					VariationType: variationDefault,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{
						description2: thisIsASimple,
						issueLink:    httpsJiraXxxGOFF,
					},
				},
			},
		},
		{
			name: "Should evaluate all flags if flag name is not provided",
			initEvaluate: func() (evaluate, error) {
				return buildEvaluate(
					&retrieverconf.RetrieverConf{Kind: file2, Path: testdataFlagGoffYaml},
					"", targetingKeyUser123, nil)
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
					TrackEvents:   true,
					VariationType: variationDefault,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{
						description2: thisIsASimple,
						issueLink:    httpsJiraXxxGOFF,
					},
				},
				"test-flag2": {
					TrackEvents:   true,
					VariationType: variationDefault,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
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
					Reason:        sTATIC,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         true,
					Cacheable:     true,
					Metadata: map[string]any{
						description2: thisIsASimple,
						issueLink:    httpsJiraXxxGOFF,
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
					RepositorySlug: thomaspoignantGoFeatureFlag,
					GithubToken:    "XXX_GH_TOKEN", //nolint:staticcheck // SA1019: the test covers the deprecated setting
					Path:           testdataFlagConfigYaml,
				}, testFlag, targetingKeyUser123, func(r retriever.Retriever) {
					gitHubRetriever, ok := r.(*githubretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *githubretriever.Retriever")
					gitHubRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
					TrackEvents:   true,
					VariationType: falseVar,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
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
					RepositorySlug: thomaspoignantGoFeatureFlag,
					AuthToken:      "XXX",
					Path:           testdataFlagConfigYaml,
				}, testFlag, targetingKeyUser123, func(r retriever.Retriever) {
					gitLabRetriever, ok := r.(*gitlabretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *gitlabretriever.Retriever")
					gitLabRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
					TrackEvents:   true,
					VariationType: falseVar,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
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
					RepositorySlug: thomaspoignantGoFeatureFlag,
					AuthToken:      "XXX",
					Path:           testdataFlagConfigYaml,
				}, testFlag, targetingKeyUser123, func(r retriever.Retriever) {
					bitBucketRetriever, ok := r.(*bitbucketretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *bitbucketretriever.Retriever")
					bitBucketRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
					TrackEvents:   true,
					VariationType: falseVar,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
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
				downloader := &testutils.S3ManagerV2Mock{
					TestDataLocation: "./testdata",
				}

				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:   "s3",
					Bucket: "Bucket",
					Item:   "valid",
				}, testFlag, targetingKeyUser123, func(r retriever.Retriever) {
					s3Retriever, ok := r.(*s3retrieverv2.Retriever)
					assert.True(t, ok, "failed to assert retriever to *s3retrieverv2.Retriever")
					s3Retriever.SetDownloader(downloader)
					_ = s3Retriever.Init(context.Background(), nil)
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
					TrackEvents:   true,
					VariationType: variationDefault,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{description2: thisIsASimple,
						issueLink: httpsJiraXxxGOFF},
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
				}, testFlag, targetingKeyUser123, func(r retriever.Retriever) {
					httpRetriever, ok := r.(*httpretriever.Retriever)
					assert.True(t, ok, "failed to assert retriever to *httpretriever.Retriever")
					httpRetriever.SetHTTPClient(&mock.HTTP{})
				})
			},
			wantErr: assert.NoError,
			expectedResult: map[string]model.RawVarResult{
				testFlag: {
					TrackEvents:   true,
					VariationType: falseVar,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
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
				mockedStorage := testutils.NewMockedGCS(t)
				mockedStorage.WithFiles(t, "flags", map[string]string{testdataFlagConfigYaml: "flag-config.yaml"})

				return buildEvaluate(&retrieverconf.RetrieverConf{
					Kind:   "googleStorage",
					Bucket: "flags",
					Object: "flag-config.yaml",
				}, testFlag, targetingKeyUser123, func(r retriever.Retriever) {
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
				testFlag: {
					TrackEvents:   true,
					VariationType: variationDefault,
					Failed:        false,
					Version:       "",
					Reason:        dEFAULT,
					ErrorCode:     "",
					ErrorDetails:  "",
					Value:         false,
					Cacheable:     true,
					Metadata: map[string]any{description2: thisIsASimple,
						issueLink: httpsJiraXxxGOFF},
				},
			},
		},
	})
}
