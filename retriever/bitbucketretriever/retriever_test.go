package bitbucketretriever_test

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
	"github.com/blackbirdworks/semaphore/retriever/bitbucketretriever"
	"github.com/blackbirdworks/semaphore/testutils/mock"
)

const (
	flagsConfigGoffYaml     = "flags/config.goff.yaml"
	gofeatureflagConfigRepo = "gofeatureflag/config-repo"
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

func Test_bitbucket_Retrieve(t *testing.T) {
	t.Parallel()

	endRatelimit := clocktest.Instant().Add(1 * time.Hour)

	type fields struct {
		httpClient     mock.HTTP
		repositorySlug string
		filePath       string
		bitbucketToken string
		branch         string
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
				repositorySlug: gofeatureflagConfigRepo,
				filePath:       flagsConfigGoffYaml,
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
		{
			name: "Success with context",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: gofeatureflagConfigRepo,
				filePath:       flagsConfigGoffYaml,
				withCtx:        true,
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
		{
			name: "HTTP Error",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: gofeatureflagConfigRepo,
				filePath:       "flags/error",
			},
			wantErr: true,
		},
		{
			name: "Error missing slug",
			fields: fields{
				httpClient: mock.HTTP{},
				filePath:   "tests/__init__.py",
				branch:     "main",
			},
			wantErr: true,
		},
		{
			name: "Error missing file path",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: gofeatureflagConfigRepo,
				filePath:       "",
			},
			wantErr: true,
		},
		{
			name: "Rate limiting",
			fields: fields{
				httpClient:     mock.HTTP{RateLimit: true, EndRatelimit: endRatelimit},
				repositorySlug: gofeatureflagConfigRepo,
				filePath:       flagsConfigGoffYaml,
			},
			wantErr: true,
			errMsg: "request to https://api.bitbucket.org/2.0/repositories/gofeatureflag/config-repo/src/main/flags/config" +
				".goff.yaml failed with code 429. Bitbucket Headers: map[X-Content-Type-Options:nosniff " +
				"X-Frame-Options:deny X-Github-Media-Type:github.v3; format=json X-Github-Request-Id:F82" +
				"D:37B98C:232EF263:235C93BD:6650BDC6 X-Ratelimit-Limit:60 X-Ratelimit-Remaining:0 X-Rate" +
				"limit-Reset:" + strconv.FormatInt(
				endRatelimit.Unix(),
				10,
			) + " X-Ratelimit-Resource:core X-Ratelimit-Used:60 X-Xss-Protection:1; mode=block]",
		},
		{
			name: "Use Bitbucket token",
			fields: fields{
				httpClient:     mock.HTTP{},
				repositorySlug: gofeatureflagConfigRepo,
				filePath:       flagsConfigGoffYaml,
				bitbucketToken: "XXX_BITBUCKET_TOKEN",
			},
			want:    []byte(sampleText()),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ret := bitbucketretriever.Retriever{
				RepositorySlug: tt.fields.repositorySlug,
				Branch:         tt.fields.branch,
				FilePath:       tt.fields.filePath,
				BitBucketToken: tt.fields.bitbucketToken,
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
				"retrieve() error = %v wantErr %v",
				err,
				tt.wantErr,
			)

			if !tt.wantErr {
				assert.Equal(t, http.MethodGet, tt.fields.httpClient.Req.Method)
				assert.Equal(t, strings.TrimSpace(string(tt.want)), strings.TrimSpace(string(got)))

				if tt.fields.bitbucketToken != "" {
					assert.Equal(
						t,
						"Bearer "+tt.fields.bitbucketToken,
						tt.fields.httpClient.Req.Header.Get("Authorization"),
					)
				}
			}
		})
	}
}
