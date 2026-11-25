package githubretriever_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/retriever/githubretriever"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/mock"
)

func Test_github_Retrieve(t *testing.T) {
	t.Parallel()

	endRatelimit := clocktest.Instant().Add(1 * time.Hour)

	type fields struct {
		httpClient     mock.HTTP
		repositorySlug string
		filePath       string
		githubToken    string
		withCtx        bool
	}

	tests := []struct {
		fields  fields
		name    string
		errMsg  string
		want    []byte
		wantErr bool
	}{
		{
			name: "Success",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       fixtures.FlagConfigYAMLPath,
			},
			want:    []byte(fixtures.TestFlagTrueVarYAML),
			wantErr: false,
		},
		{
			name: "Success with context",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       fixtures.FlagConfigYAMLPath,
				withCtx:        true,
			},
			want:    []byte(fixtures.TestFlagTrueVarYAML),
			wantErr: false,
		},
		{
			name: "Success with default method",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       fixtures.FlagConfigYAMLPath,
			},
			want:    []byte(fixtures.TestFlagTrueVarYAML),
			wantErr: false,
		},
		{
			name: "HTTP Error",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       "testdata/error",
			},
			wantErr: true,
		},
		{
			name: "Error missing slug",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: "",
				filePath:       fixtures.FlagConfigYAMLPath,
			},
			wantErr: true,
		},
		{
			name: "Error missing file path",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       "",
			},
			wantErr: true,
		},
		{
			name: "Ratelimiting",
			fields: fields{
				httpClient:     mock.HTTP{RateLimit: true, EndRatelimit: endRatelimit},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       fixtures.FlagConfigYAMLPath,
			},
			errMsg: "request to https://api.github.com/repos/thomaspoignant/go-feature-flag/contents/testdata/flag-config." +
				"yaml?ref=main failed with code 429. GitHub Headers: map[X-Content-Type-Options:nosniff " +
				"X-Frame-Options:deny X-Github-Media-Type:github.v3; format=json X-Github-Request-Id:F82" +
				"D:37B98C:232EF263:235C93BD:6650BDC6 X-Ratelimit-Limit:60 X-Ratelimit-Remaining:0 X-Rate" +
				"limit-Reset:" + strconv.FormatInt(
				endRatelimit.Unix(),
				10,
			) + " X-Ratelimit-Resource:core X-Ratelimit-Used:60 X-Xss-Protection:1; mode=block]",
			wantErr: true,
		},
		{
			name: "Use GitHub token",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       fixtures.FlagConfigYAMLPath,
				githubToken:    "XXX_GH_TOKEN",
			},
			want:    []byte(fixtures.TestFlagTrueVarYAML),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ret := githubretriever.Retriever{
				RepositorySlug: tt.fields.repositorySlug,
				FilePath:       tt.fields.filePath,
				GithubToken:    tt.fields.githubToken,
			}

			ret.SetHTTPClient(&tt.fields.httpClient)

			var ctx context.Context
			if tt.fields.withCtx {
				ctx = t.Context()
			}

			got, err := ret.Retrieve(ctx)
			if tt.errMsg != "" {
				require.EqualError(t, err, tt.errMsg)
			}

			assert.Equal(
				t,
				tt.wantErr,
				err != nil,
				"retrieve() error = %v, wantErr %v",
				err,
				tt.wantErr,
			)

			if !tt.wantErr {
				assert.Equal(t, http.MethodGet, tt.fields.httpClient.Req.Method)
				assert.Equal(t, tt.want, got)

				if tt.fields.githubToken != "" {
					assert.Equal(
						t,
						protocol.BearerPrefix+tt.fields.githubToken,
						tt.fields.httpClient.Req.Header.Get(protocol.HeaderAuthorization),
					)
				}
			}
		})
	}
}

func Test_github_Retrieve_BaseURL(t *testing.T) {
	t.Parallel()

	type fields struct {
		httpClient     mock.HTTP
		repositorySlug string
		filePath       string
		githubToken    string
		baseURL        string
		branch         string
	}

	tests := []struct {
		fields      fields
		name        string
		expectedURL string
		want        []byte
		wantErr     bool
	}{
		{
			name: "GitHub Enterprise base URL",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoMyOrg,
				filePath:       fixtures.ConfigFlagsYAML,
				githubToken:    ghpToken,
				baseURL:        "https://github.acme.com/api/v3",
			},
			want:        []byte(fixtures.TestFlagTrueVarYAML),
			wantErr:     false,
			expectedURL: "https://github.acme.com/api/v3/repos/myorg/myrepo/contents/config/flags.yaml?ref=main",
		},
		{
			name: "Default GitHub API URL (empty BaseURL)",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoGoFeatureFlag,
				filePath:       fixtures.FlagConfigYAMLPath,
			},
			want:    []byte(fixtures.TestFlagTrueVarYAML),
			wantErr: false,
			expectedURL: "https://api.github.com/repos/thomaspoignant/go-feature-flag/contents/testdata/flag-config.yaml?r" +
				"ef=main",
		},
		{
			name: "GitHub Enterprise with different branch",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoMyOrg,
				filePath:       fixtures.ConfigFlagsYAML,
				baseURL:        "https://github.enterprise.com/api/v3",
				branch:         "develop",
			},
			want:        []byte(fixtures.TestFlagTrueVarYAML),
			wantErr:     false,
			expectedURL: "https://github.enterprise.com/api/v3/repos/myorg/myrepo/contents/config/flags.yaml?ref=develop",
		},
		{
			name: "GitHub Enterprise with trailing slash in BaseURL",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: fixtures.RepoMyOrg,
				filePath:       fixtures.ConfigFlagsYAML,
				baseURL:        "https://github.acme.com/api/v3/",
			},
			want:        []byte(fixtures.TestFlagTrueVarYAML),
			wantErr:     false,
			expectedURL: "https://github.acme.com/api/v3/repos/myorg/myrepo/contents/config/flags.yaml?ref=main",
		},
		{
			name: "Invalid base URL",
			fields: fields{
				repositorySlug: fixtures.RepoMyOrg,
				filePath:       fixtures.ConfigFlagsYAML,
				baseURL:        ":invalid:url",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Path traversal in FilePath",
			fields: fields{
				repositorySlug: fixtures.RepoMyOrg,
				filePath:       "../../../etc/passwd",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "Path traversal in RepositorySlug",
			fields: fields{
				repositorySlug: "../../../etc/passwd",
				filePath:       fixtures.ConfigFlagsYAML,
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ret := githubretriever.Retriever{
				RepositorySlug: tt.fields.repositorySlug,
				FilePath:       tt.fields.filePath,
				GithubToken:    tt.fields.githubToken,
				BaseURL:        tt.fields.baseURL,
				Branch:         tt.fields.branch,
			}

			ret.SetHTTPClient(&tt.fields.httpClient)
			got, err := ret.Retrieve(context.Background())
			assert.Equal(t, tt.wantErr, err != nil, "retrieve() error = %v, wantErr %v", err, tt.wantErr)

			if !tt.wantErr {
				assert.Equal(t, string(tt.want), string(got))
				assert.Equal(t, tt.expectedURL, tt.fields.httpClient.Req.URL.String())

				if tt.fields.githubToken != "" {
					assert.Equal(
						t,
						protocol.BearerPrefix+tt.fields.githubToken,
						tt.fields.httpClient.Req.Header.Get(protocol.HeaderAuthorization),
					)
				}
			}
		})
	}
}

func TestRateLimiting(t *testing.T) {
	t.Parallel()

	ret := githubretriever.Retriever{
		RepositorySlug: fixtures.RepoGoFeatureFlag,
		FilePath:       fixtures.FlagConfigYAMLPath,
	}

	httpClient := &mock.HTTP{}
	ret.SetHTTPClient(httpClient)
	_, err := ret.Retrieve(context.TODO())
	require.NoError(t, err)
	assert.True(t, httpClient.HasBeenCalled)

	httpClient = &mock.HTTP{RateLimit: true}
	ret.SetHTTPClient(httpClient)
	_, err = ret.Retrieve(context.TODO())
	require.Error(t, err)
	assert.True(t, httpClient.HasBeenCalled)

	httpClient = &mock.HTTP{}
	ret.SetHTTPClient(httpClient)
	_, err = ret.Retrieve(context.TODO())
	require.Error(t, err)
	assert.False(t, httpClient.HasBeenCalled)
}

func Test_github_Retrieve_InvalidBaseURLDoesNotLeak(t *testing.T) {
	t.Parallel()

	h := githubretriever.Retriever{
		BaseURL:        "https://user:SECRETMARKER@github.example.com:bad/?token=SECRETMARKER",
		FilePath:       "flags.yaml",
		RepositorySlug: "org/repo",
	}
	_, err := h.Retrieve(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), invalidBaseURL)
	assert.NotContains(t, err.Error(), "SECRETMARKER")
}
