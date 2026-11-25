package githubretriever

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blackbirdworks/semaphore/internal"
	"github.com/blackbirdworks/semaphore/modules/core/clock"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/shared"
)

const (
	// maxErrorBodyBytes is how much of an error response is read to build the error message.
	maxErrorBodyBytes = 512
)

var errRequestTo = errors.New("request to")

var (
	errGithubAppGithubTokenAnd = errors.New(
		"github app: GithubToken and GitHub App fields are mutually exclusive",
	)
	errGithubAppAppIDInstallationID = errors.New(
		"github app: AppID, InstallationID and PrivateKey or PrivateKeyFile are all required",
	)
	errMissingMandatoryInformationFilePath = errors.New("missing mandatory information filePath")
	errGithubAppAccessTokens               = errors.New("github app: access_tokens endpoint returned")
	errGithubAppTokenResponse              = errors.New("github app: token response contained no token")
	errGithubAppTokenResponseContained     = errors.New("github app: token response contained no expires_at")
	errGithubAppFailedTo                   = errors.New("github app: failed to decode PEM block from private key")
	errRateLimitExceededNext               = errors.New("rate limit exceeded. Next call will be after")
	errFilepathMustNotContain              = errors.New("filepath must not contain '..'")
	errRepositorySlugMustNot               = errors.New("repository slug must not contain '..'")
)

// Retriever is a configuration struct for a GitHub retriever.
type Retriever struct {
	rateLimitReset time.Time
	tokenExpiresAt time.Time
	// httpClient is the http.Client if you want to override it.
	httpClient     internal.HTTPClient
	Branch         string // default is main
	FilePath       string
	GithubToken    string
	RepositorySlug string
	// BaseURL is the base URL for the GitHub API.
	// If not specified, it defaults to "https://api.github.com" for GitHub.com.
	// For GitHub Enterprise instances, specify your instance URL (e.g., "https://github.acme.com/api/v3").
	BaseURL string
	// cached installation token state (GitHub App auth only)
	cachedToken    string
	PrivateKey     string // PEM-encoded RSA private key
	PrivateKeyFile string // path to a PEM file; takes precedence over PrivateKey if both are set
	status         retriever.Status
	Timeout        time.Duration // default is 10 seconds
	// rate limit fields
	rateLimitRemaining int
	InstallationID     int64
	// GitHub App authentication fields.
	// AppID and InstallationID are required together with either
	// PrivateKey (PEM string) or PrivateKeyFile (path to a PEM file).
	// Mutually exclusive with GithubToken / AuthToken.
	AppID int64
	// mu guards the rate limit fields and the cached installation token,
	// so concurrent Retrieve calls mint the token only once.
	mu sync.Mutex
}

// installationTokenResponse is the JSON body returned by the access_tokens endpoint.
type installationTokenResponse struct {
	ExpiresAt time.Time `json:"expires_at"` //nolint:tagliatelle // GitHub REST API (installation access token)
	Token     string    `json:"token"`
}

// Init validates the authentication configuration.
func (r *Retriever) Init(_ context.Context, _ *slog.Logger) error {
	r.status = retriever.RetrieverNotReady

	err := r.validateAppAuth()
	if err != nil {
		r.status = retriever.RetrieverError

		return err
	}

	r.status = retriever.RetrieverReady

	return nil
}

// Shutdown drops the cached installation token and sets the status as not ready.
func (r *Retriever) Shutdown(_ context.Context) error {
	r.invalidateCachedToken()
	r.status = retriever.RetrieverNotReady

	return nil
}

// Status is the function returning the internal state of the retriever.
func (r *Retriever) Status() retriever.Status {
	if r == nil || r.status == "" {
		return retriever.RetrieverNotReady
	}

	return r.status
}

// Retrieve is the function in charge of fetching the flag configuration.
func (r *Retriever) Retrieve(ctx context.Context) ([]byte, error) {
	if r.FilePath == "" || r.RepositorySlug == "" {
		return nil, fmt.Errorf("%w=%s, repositorySlug=%s", errMissingMandatoryInformationFilePath,
			r.FilePath,
			r.RepositorySlug,
		)
	}

	// default branch is main
	branch := r.Branch
	if branch == "" {
		branch = "main"
	}

	token, err := r.resolveToken(ctx)
	if err != nil {
		return nil, err
	}

	checkRateLimitErr := r.checkRateLimit()
	if checkRateLimitErr != nil {
		return nil, checkRateLimitErr
	}

	requestURL, err := r.buildURL(branch)
	if err != nil {
		return nil, err
	}

	resp, err := r.fetchWithRetry(ctx, requestURL, token)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	checkResponseErrorErr := r.checkResponseError(resp, requestURL)
	if checkResponseErrorErr != nil {
		return nil, checkResponseErrorErr
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read GitHub response body: %w", err)
	}

	return body, nil
}

// fetchWithRetry fetches the URL. On a 401 with App auth, the cached token may have just expired: it is
// invalidated and the request is retried once with a fresh token.
func (r *Retriever) fetchWithRetry(ctx context.Context, requestURL, token string) (*http.Response, error) {
	resp, err := r.fetch(ctx, requestURL, token)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized && r.isAppAuth() {
		_ = resp.Body.Close()

		r.invalidateCachedToken()

		token, err = r.resolveToken(ctx)
		if err != nil {
			return nil, err
		}

		return r.fetch(ctx, requestURL, token)
	}

	return resp, nil
}

// SetHTTPClient is here if you want to override the default [http.Client] we are using.
// It is also used for the tests.
func (r *Retriever) SetHTTPClient(client internal.HTTPClient) {
	r.httpClient = client
}

// validateAppAuth checks that GitHub App fields are either all unset, or complete,
// usable and not mixed with GithubToken.
func (r *Retriever) validateAppAuth() error {
	if r.AppID == 0 && r.InstallationID == 0 && r.PrivateKey == "" && r.PrivateKeyFile == "" {
		return nil
	}

	if r.GithubToken != "" {
		return errGithubAppGithubTokenAnd
	}

	if !r.isAppAuth() {
		return errGithubAppAppIDInstallationID
	}

	_, err := r.loadPrivateKey()

	return err
}

// fetch calls the contents API with the given token and records the rate limit headers.
func (r *Retriever) fetch(ctx context.Context, contentsURL, token string) (*http.Response, error) {
	resp, err := shared.CallHTTPAPI(
		ctx,
		contentsURL,
		http.MethodGet,
		"",
		r.Timeout,
		r.buildHeaders(token),
		r.httpClient,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch GitHub contents: %w", err)
	}

	r.updateRateLimit(resp.Header)

	return resp, nil
}

// isAppAuth reports whether GitHub App auth fields are configured.
func (r *Retriever) isAppAuth() bool {
	return r.AppID != 0 && r.InstallationID != 0 && (r.PrivateKey != "" || r.PrivateKeyFile != "")
}

// resolveToken returns the bearer token to use for the current request.
// For PAT auth it returns GithubToken directly.
// For App auth it returns a cached installation token, refreshing it when needed.
func (r *Retriever) resolveToken(ctx context.Context) (string, error) {
	if !r.isAppAuth() {
		return r.GithubToken, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Refresh ~5 minutes before expiry so we never send an about-to-expire token.
	if r.cachedToken != "" && clock.Now().Before(r.tokenExpiresAt.Add(-5*time.Minute)) {
		return r.cachedToken, nil
	}

	token, expiresAt, err := r.mintInstallationToken(ctx)
	if err != nil {
		return "", fmt.Errorf("github app: failed to mint installation token: %w", err)
	}

	r.cachedToken = token
	r.tokenExpiresAt = expiresAt

	return token, nil
}

// invalidateCachedToken clears the cached installation token so the next call to
// resolveToken mints a fresh one.
func (r *Retriever) invalidateCachedToken() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cachedToken = ""
	r.tokenExpiresAt = time.Time{}
}

// mintInstallationToken signs a JWT, exchanges it for an installation token, and
// returns the token string and its expiry time.
func (r *Retriever) mintInstallationToken(ctx context.Context) (string, time.Time, error) {
	key, err := r.loadPrivateKey()
	if err != nil {
		return "", time.Time{}, err
	}

	jwt, err := signAppJWT(r.AppID, key)
	if err != nil {
		return "", time.Time{}, err
	}

	parsedURL, err := r.apiURL("app", "installations", strconv.FormatInt(r.InstallationID, 10), "access_tokens")
	if err != nil {
		return "", time.Time{}, err
	}

	// access_tokens answers in JSON, not in the raw format used for file contents.
	header := r.buildHeaders(jwt)
	header.Set("Accept", "application/vnd.github+json")

	resp, err := shared.CallHTTPAPI(ctx, parsedURL.String(), http.MethodPost, "", r.Timeout, header, r.httpClient)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("request GitHub access token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		// GitHub explains the failure in the body (bad app id, suspended installation, clock skew...).
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))

		return "", time.Time{}, fmt.Errorf("%w %d for installation %d: %s", errGithubAppAccessTokens,
			resp.StatusCode, r.InstallationID, strings.TrimSpace(string(msg)),
		)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read GitHub access token response: %w", err)
	}

	var result installationTokenResponse

	unmarshalErr := json.Unmarshal(body, &result)
	if unmarshalErr != nil {
		return "", time.Time{}, fmt.Errorf("github app: failed to parse token response: %w", unmarshalErr)
	}

	if result.Token == "" {
		return "", time.Time{}, errGithubAppTokenResponse
	}

	if result.ExpiresAt.IsZero() {
		return "", time.Time{}, errGithubAppTokenResponseContained
	}

	return result.Token, result.ExpiresAt, nil
}

// loadPrivateKey returns the RSA private key from PrivateKeyFile (preferred) or PrivateKey.
func (r *Retriever) loadPrivateKey() (*rsa.PrivateKey, error) {
	var (
		pemBytes []byte
		err      error
	)

	if r.PrivateKeyFile != "" {
		pemBytes, err = os.ReadFile(r.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("github app: failed to read private key file %s: %w", r.PrivateKeyFile, err)
		}
	} else {
		pemBytes = []byte(r.PrivateKey)
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errGithubAppFailedTo
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app: failed to parse RSA private key: %w", err)
	}

	return key, nil
}

// buildHeaders constructs the HTTP headers for the GitHub API request.
func (*Retriever) buildHeaders(token string) http.Header {
	header := http.Header{}
	header.Add("Accept", "application/vnd.github.raw")
	header.Add("X-Github-Api-Version", "2022-11-28")

	if token != "" {
		header.Add("Authorization", "Bearer "+token)
	}

	return header
}

// checkRateLimit checks if the rate limit has been exceeded.
func (r *Retriever) checkRateLimit() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.rateLimitRemaining <= 0 && clock.Now().Before(r.rateLimitReset) {
		return fmt.Errorf("%w %s", errRateLimitExceededNext, r.rateLimitReset)
	}

	return nil
}

// buildURL constructs the GitHub API URL for retrieving the file.
func (r *Retriever) buildURL(branch string) (string, error) {
	// Validate inputs to prevent path traversal attacks
	if strings.Contains(r.FilePath, "..") {
		return "", errFilepathMustNotContain
	}

	if strings.Contains(r.RepositorySlug, "..") {
		return "", errRepositorySlugMustNot
	}

	parsedURL, err := r.apiURL("repos", r.RepositorySlug, "contents", r.FilePath)
	if err != nil {
		return "", err
	}

	q := parsedURL.Query()
	q.Set("ref", branch)
	parsedURL.RawQuery = q.Encode()

	return parsedURL.String(), nil
}

// apiURL returns the GitHub API base URL (BaseURL or api.github.com) with elem appended to its path.
func (r *Retriever) apiURL(elem ...string) (*url.URL, error) {
	baseURL := r.BaseURL
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", internal.RedactURLError(err))
	}

	parsedURL.Path = path.Join(append([]string{parsedURL.Path}, elem...)...)

	return parsedURL, nil
}

// checkResponseError checks if the response indicates an error and returns an appropriate error message.
func (*Retriever) checkResponseError(resp *http.Response, rawURL string) error {
	if resp.StatusCode >= http.StatusBadRequest {
		// Collect the headers to add in the error message
		ghHeaders := map[string]string{}

		for name := range resp.Header {
			if strings.HasPrefix(name, "X-") {
				ghHeaders[name] = resp.Header.Get(name)
			}
		}

		return fmt.Errorf("%w %s failed with code %d. GitHub Headers: %v",
			errRequestTo, rawURL, resp.StatusCode, ghHeaders)
	}

	return nil
}

func (r *Retriever) updateRateLimit(headers http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if remaining := headers.Get("X-Ratelimit-Remaining"); remaining != "" {
		remainingInt, err := strconv.Atoi(remaining)
		if err == nil {
			r.rateLimitRemaining = remainingInt
		}
	}

	if reset := headers.Get("X-Ratelimit-Reset"); reset != "" {
		resetInt, err := strconv.ParseInt(reset, 10, 64)
		if err == nil {
			r.rateLimitReset = time.Unix(resetInt, 0)
		}
	}
}

// compile-time check of the interface implemented by the type.
var _ retriever.InitializableRetriever = (*Retriever)(nil)
