package gitlabretriever_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/retriever/gitlabretriever"
	"github.com/blackbirdworks/semaphore/testutils/mock"
)

const (
	aaGoFeatureFlags = "aa/go-feature-flags-config"
	featureAddFlags  = "feature/add+flags"
	flagConfigYaml   = "flag-config.yaml"
	flagsYaml        = "flags.yaml"
	httpsGitlabCom   = "https://gitlab.com"
	refFeature2Fadd  = "ref=feature%2Fadd%2Bflags"
)

func sampleText() string {
	return `test-flag:
  variations:
    true_var: true
    false_var: false
  targeting:
    - query: key eq "random-key"
      percentage:
        true_var: 0
        false_var: 100
  defaultRule:
    variation: false_var
`
}
func Test_gitlab_Retrieve(t *testing.T) {
	t.Parallel()

	type fields struct {
		httpClient     mock.HTTP
		filePath       string
		gitlabToken    string
		repositorySlug string
		baseURL        string
		branch         string
		withCtx        bool
	}

	tests := []struct {
		name    string
		fields  fields
		want    []byte
		wantErr bool
	}{
		{
			name: "Success",
			fields: fields{
				httpClient:     mock.HTTP{},
				baseURL:        httpsGitlabCom,
				repositorySlug: aaGoFeatureFlags,
				filePath:       flagConfigYaml,
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
		{
			name: "Success with context",
			fields: fields{
				httpClient:     mock.HTTP{},
				baseURL:        httpsGitlabCom,
				repositorySlug: aaGoFeatureFlags,
				filePath:       flagConfigYaml,
				withCtx:        true,
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
		{
			name: "Success with default method",
			fields: fields{
				httpClient:     mock.HTTP{},
				baseURL:        httpsGitlabCom,
				repositorySlug: aaGoFeatureFlags,
				filePath:       flagConfigYaml,
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
		{
			name: "HTTP Error",
			fields: fields{
				httpClient:     mock.HTTP{},
				baseURL:        "https://gitlab.com/error",
				repositorySlug: aaGoFeatureFlags,
				filePath:       "bad-file/file.yaml",
				branch:         "error",
			},
			wantErr: true,
		},
		{
			name: "Error missing slug",
			fields: fields{
				httpClient: mock.HTTP{},
				baseURL:    "",
				filePath:   flagConfigYaml,
			},
			wantErr: true,
		},
		{
			name: "Error missing file path",
			fields: fields{
				httpClient: mock.HTTP{},
				baseURL:    "https://gitlab.com/",
				filePath:   "",
			},
			wantErr: true,
		},
		{
			name: "Use gitlab token",
			fields: fields{
				httpClient:     mock.HTTP{},
				baseURL:        httpsGitlabCom,
				filePath:       flagConfigYaml,
				gitlabToken:    "XXX",
				repositorySlug: aaGoFeatureFlags,
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
		{
			name: "Impossible to parse URL",
			fields: fields{
				httpClient:     mock.HTTP{},
				baseURL:        "https://user:abc{DEf1=ghi@example.com:5432/",
				filePath:       flagConfigYaml,
				gitlabToken:    "XXX",
				repositorySlug: aaGoFeatureFlags,
			},
			want:    []byte(sampleText()),
			wantErr: true,
		},
		{
			name: "Use default values",
			fields: fields{
				httpClient:     mock.HTTP{},
				filePath:       flagConfigYaml,
				repositorySlug: aaGoFeatureFlags,
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ret := gitlabretriever.Retriever{
				BaseURL:        tt.fields.baseURL,
				FilePath:       tt.fields.filePath,
				GitlabToken:    tt.fields.gitlabToken,
				Branch:         tt.fields.branch,
				RepositorySlug: tt.fields.repositorySlug,
			}
			ret.SetHTTPClient(&tt.fields.httpClient)

			var ctx context.Context
			if tt.fields.withCtx {
				ctx = t.Context()
			}

			got, err := ret.Retrieve(ctx)
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
				assert.Equal(t, strings.TrimSpace(string(tt.want)), strings.TrimSpace(string(got)))

				if tt.fields.gitlabToken != "" {
					assert.Equal(
						t,
						tt.fields.gitlabToken,
						tt.fields.httpClient.Req.Header.Get("Private-Token"),
					)
				}
			}
		})
	}
}

func Test_gitlab_RetrievePathEscaping(t *testing.T) {
	t.Parallel()

	const projectPath = "/api/v4/projects/team%2Fsubgroup%2Fconfig/repository/files/"

	tests := []struct {
		name            string
		filePath        string
		branch          string
		wantEscapedPath string
		wantRawQuery    string
	}{
		{
			name:            "simple filename",
			filePath:        flagsYaml,
			branch:          featureAddFlags,
			wantEscapedPath: projectPath + "flags.yaml/raw",
			wantRawQuery:    refFeature2Fadd,
		},
		{
			name:            "nested path",
			filePath:        "config/flags.yaml",
			branch:          featureAddFlags,
			wantEscapedPath: projectPath + "config%2Fflags.yaml/raw",
			wantRawQuery:    refFeature2Fadd,
		},
		{
			name:            "spaces in directory and filename",
			filePath:        "flag configs/my flags.yaml",
			branch:          featureAddFlags,
			wantEscapedPath: projectPath + "flag%20configs%2Fmy%20flags.yaml/raw",
			wantRawQuery:    refFeature2Fadd,
		},
		{
			name:            "literal plus sign",
			filePath:        "flags/my+flags.yaml",
			branch:          featureAddFlags,
			wantEscapedPath: projectPath + "flags%2Fmy%2Bflags.yaml/raw",
			wantRawQuery:    refFeature2Fadd,
		},
		{
			name:            "literal percent encoding",
			filePath:        "flags/my%20flags.yaml",
			branch:          featureAddFlags,
			wantEscapedPath: projectPath + "flags%2Fmy%2520flags.yaml/raw",
			wantRawQuery:    refFeature2Fadd,
		},
		{
			name:            "query and fragment delimiters",
			filePath:        "flags/my?#flags.yaml",
			branch:          featureAddFlags,
			wantEscapedPath: projectPath + "flags%2Fmy%3F%23flags.yaml/raw",
			wantRawQuery:    refFeature2Fadd,
		},
		{
			name:            "non-ASCII filename",
			filePath:        "flags/caf\u00e9.yaml",
			branch:          featureAddFlags,
			wantEscapedPath: projectPath + "flags%2Fcaf%C3%A9.yaml/raw",
			wantRawQuery:    refFeature2Fadd,
		},
		{
			name:            "default branch",
			filePath:        flagsYaml,
			wantEscapedPath: projectPath + "flags.yaml/raw",
			wantRawQuery:    "ref=main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &mock.HTTP{}
			r := gitlabretriever.Retriever{
				RepositorySlug: "team/subgroup/config",
				FilePath:       tt.filePath,
				Branch:         tt.branch,
			}
			r.SetHTTPClient(client)

			_, err := r.Retrieve(context.Background())
			require.NoError(t, err)
			// EscapedPath is what is sent on the wire, URL.Path would hide the encoding.
			assert.Equal(t, tt.wantEscapedPath, client.Req.URL.EscapedPath())
			assert.Equal(t, tt.wantRawQuery, client.Req.URL.RawQuery)
			assert.Empty(t, client.Req.URL.Fragment)
		})
	}
}

func Test_gitlab_RetrieveInvalidBaseURLDoesNotLeak(t *testing.T) {
	t.Parallel()

	h := gitlabretriever.Retriever{
		BaseURL:        "https://user:SECRETMARKER@gitlab.example.com:bad/?token=SECRETMARKER",
		FilePath:       flagsYaml,
		RepositorySlug: "team/config",
	}
	_, err := h.Retrieve(context.Background())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETMARKER")
}
