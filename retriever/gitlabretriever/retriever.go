package gitlabretriever

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/blackbirdworks/semaphore/internal"
	httpretriever "github.com/blackbirdworks/semaphore/retriever/httpretriever"
)

var (
	errMissingMandatoryInformationFilePath = errors.New("missing mandatory information filePath")
)

// Retriever is a configuration struct for a GitHub retriever.
type Retriever struct {
	// Branch is the name of the GITLAB branch where to download the file
	// default: main
	Branch string // default is main
	// FilePath is the location of your file in the repository
	FilePath string

	// GitlabToken is the token to use when downloading the file
	GitlabToken string

	// RepositorySlug is the name of your repository in your gitlab instance
	// ex: thomaspoignant/go-feature-flag
	RepositorySlug string

	// BaseURL is the DNS of your GITLAB installation.
	// default: https://gitlab.com
	BaseURL string

	// Timeout is the time before we timeout while retrieving the flag file.
	// default: 10 seconds
	Timeout time.Duration

	// httpClient is the http.Client if you want to override it.
	httpClient internal.HTTPClient
}

// Retrieve is the function in charge of fetching the flag configuration.
func (r *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	if r.FilePath == "" || r.RepositorySlug == "" {
		return nil, fmt.Errorf("%w=%s, repositorySlug=%s", errMissingMandatoryInformationFilePath,
			r.FilePath,
			r.RepositorySlug,
		)
	}
	branch := r.Branch
	if branch == "" {
		branch = "main"
	}
	if r.BaseURL == "" {
		r.BaseURL = "https://gitlab.com"
	}

	path := strings.Join([]string{
		r.BaseURL, "api/v4/projects",
		escapePathSegment(r.RepositorySlug),
		"repository/files",
		escapePathSegment(r.FilePath), "raw"}, "/")

	parsedURL, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("impossible to parse the url %w", internal.RedactURLError(err))
	}

	rawQuery := parsedURL.Query()
	rawQuery.Set("ref", branch)
	parsedURL.RawQuery = rawQuery.Encode()

	// add header for Gitlab Token if specified
	header := http.Header{}
	if r.GitlabToken != "" {
		header.Add("Private-Token", r.GitlabToken)
	}
	httpRetriever := httpretriever.Retriever{
		URL:     parsedURL.String(),
		Method:  http.MethodGet,
		Header:  header,
		Timeout: r.Timeout,
	}

	if r.httpClient != nil {
		httpRetriever.SetHTTPClient(r.httpClient)
	}

	return httpRetriever.Retrieve(ctx)
}

// escapePathSegment encodes s as a single URL path segment, as expected by the GitLab API.
// [url.PathEscape] leaves "+" as is, but some servers and proxies decode it as a space,
// so we keep encoding it as "%2B".
func escapePathSegment(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), "+", "%2B")
}

// SetHTTPClient is here if you want to override the default [http.Client] we are using.
// It is also used for the tests.
func (r *Retriever) SetHTTPClient(client internal.HTTPClient) {
	r.httpClient = client
}
