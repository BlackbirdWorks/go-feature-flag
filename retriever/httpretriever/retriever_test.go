package httpretriever_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/retriever/httpretriever"
	"github.com/blackbirdworks/semaphore/testutils/mock"
)

const (
	applicationJSON          = "application/json"
	contentType              = "Content-Type"
	httpLocalhostExampleFile = "http://localhost.example/file"
	testFlagVariationsTrue   = `test-flag:
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
)

func Test_httpRetriever_Retrieve(t *testing.T) {
	t.Parallel()

	type fields struct {
		httpClient mock.HTTP
		header     http.Header
		url        string
		method     string
		body       string
		withCtx    bool
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
				httpClient: mock.HTTP{},
				url:        httpLocalhostExampleFile,
				method:     http.MethodGet,
				body:       "",
				header:     nil,
			},
			want:    []byte(testFlagVariationsTrue),
			wantErr: false,
		},
		{
			name: "Success with context",
			fields: fields{
				httpClient: mock.HTTP{},
				url:        httpLocalhostExampleFile,
				method:     http.MethodGet,
				body:       "",
				header:     nil,
				withCtx:    true,
			},
			want:    []byte(testFlagVariationsTrue),
			wantErr: false,
		},
		{
			name: "Success with default method",
			fields: fields{
				httpClient: mock.HTTP{},
				url:        httpLocalhostExampleFile,
				body:       "",
				header:     nil,
			},
			want:    []byte(testFlagVariationsTrue),
			wantErr: false,
		},
		{
			name: "HTTP Error",
			fields: fields{
				httpClient: mock.HTTP{},
				url:        "http://localhost.example/httpError",
				method:     http.MethodPost,
				body:       "",
				header:     map[string][]string{contentType: {applicationJSON}},
			},
			wantErr: true,
		},
		{
			name: "Error",
			fields: fields{
				httpClient: mock.HTTP{},
				url:        "http://localhost.example/error",
				method:     http.MethodPost,
				body:       "",
				header:     map[string][]string{contentType: {applicationJSON}},
			},
			wantErr: true,
		},
		{
			name: "No URL",
			fields: fields{
				httpClient: mock.HTTP{},
				url:        "",
				method:     http.MethodPost,
				body:       "",
				header:     map[string][]string{contentType: {applicationJSON}},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ret := httpretriever.Retriever{
				URL:    tt.fields.url,
				Method: tt.fields.method,
				Body:   tt.fields.body,
				Header: tt.fields.header,
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

			if tt.fields.method == "" {
				assert.Equal(t, http.MethodGet, tt.fields.httpClient.Req.Method)
			}

			if !t.Failed() {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

type statusClient struct{ code int }

func (c statusClient) Do(_ *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: c.code, Body: io.NopCloser(strings.NewReader(""))}, nil
}

func TestRetriever_ErrorDoesNotLeakCredentials(t *testing.T) {
	t.Parallel()

	h := httpretriever.Retriever{
		URL: "https://user:SECRETMARKER@flags.example.com/SECRETMARKER/flags.yaml?token=SECRETMARKER",
	}
	h.SetHTTPClient(statusClient{code: http.StatusForbidden})
	_, err := h.Retrieve(context.Background())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETMARKER")
	assert.Contains(t, err.Error(), "https://flags.example.com failed")
	assert.Contains(t, err.Error(), "403")
}
