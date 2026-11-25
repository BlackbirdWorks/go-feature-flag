package githubretriever_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/testutils/clocktest"
	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/githubretriever"
)

const (
	areAllRequired = "are all required"
	authorization2 = "Authorization"
	ghpToken       = "ghp_token"
	invalidBaseURL = "invalid base URL"
)

var (
	errConnectionReset = errors.New("connection reset")
)

var _ retriever.InitializableRetriever = &githubretriever.Retriever{}

// flagBody is what the mock contents API returns.
const flagBody = `test-flag:
  variations:
    true_var: true
    false_var: false
  defaultRule:
    variation: false_var
`

const (
	testAppID          = int64(12345)
	defaultTokenURL    = "https://api.github.com/app/installations/67890/access_tokens"
	testInstallationID = int64(67890)
)

// tokenResponse builds a JSON access_tokens API response.
func tokenResponse(token string, expiresAt time.Time) []byte {
	b, err := json.Marshal(map[string]string{
		"token":      token,
		"expires_at": expiresAt.Format(time.RFC3339),
	})
	if err != nil {
		panic(err)
	}

	return b
}

// appMockHTTP is a multi-URL mock for GitHub App tests.
// It records all requests and lets tests configure per-call behaviour.
// Every minted token is unique (ghs_token_1, ghs_token_2, ...) so tests can tell which one was used.
type appMockHTTP struct {
	// retryErr is returned as a transport error for the contents request retried after a 401.
	retryErr error
	// tokenBody overrides the body of the access_tokens response.
	tokenBody    []byte
	recordedReqs []*http.Request
	// tokenStatus is the HTTP status returned for the access_tokens endpoint.
	// Defaults to 201 Created.
	tokenStatus int
	// tokenTTL is the lifetime of the minted tokens. Defaults to 1 hour.
	tokenTTL      time.Duration
	tokenCalls    int
	contentsCalls int
	mu            sync.Mutex
	// first401ThenOK makes the first contents request return 401, then 200.
	first401ThenOK bool
}

func (m *appMockHTTP) Do(req *http.Request) (*http.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordedReqs = append(m.recordedReqs, req)

	if strings.HasSuffix(req.URL.Path, "/access_tokens") {
		m.tokenCalls++

		body := m.tokenBody
		if body == nil {
			body = tokenResponse(
				fmt.Sprintf("ghs_token_%d", m.tokenCalls),
				clocktest.Instant().Add(cmp.Or(m.tokenTTL, time.Hour)),
			)
		}

		return newResponse(cmp.Or(m.tokenStatus, http.StatusCreated), body), nil
	}

	m.contentsCalls++
	if m.first401ThenOK && m.contentsCalls == 1 {
		return newResponse(http.StatusUnauthorized, nil), nil
	}

	if m.first401ThenOK && m.retryErr != nil {
		return nil, m.retryErr
	}

	resp := newResponse(http.StatusOK, []byte(flagBody))
	resp.Header.Set("X-Ratelimit-Remaining", "4999")
	resp.Header.Set("X-Ratelimit-Reset", strconv.FormatInt(clocktest.Instant().Add(time.Hour).Unix(), 10))

	return resp, nil
}

func newResponse(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
	}
}

// generateTestKey generates a throwaway RSA key for tests and returns it with its PEM encoding.
func generateTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return key, string(pemBytes)
}

// newAppRetriever returns a retriever configured for GitHub App auth.
func newAppRetriever(pemStr string, mock *appMockHTTP) *githubretriever.Retriever {
	r := &githubretriever.Retriever{
		RepositorySlug: myorgMyrepo,
		FilePath:       "flags/config.yaml",
		AppID:          testAppID,
		InstallationID: testInstallationID,
		PrivateKey:     pemStr,
	}
	r.SetHTTPClient(mock)

	return r
}

// assertAppJWT checks the JWT sent to access_tokens the way GitHub would:
// RS256 signature from the app key, iss = app ID, iat backdated ~60s, exp within 10 minutes.
func assertAppJWT(t *testing.T, authorization string, pub *rsa.PublicKey) {
	t.Helper()

	jwt, ok := strings.CutPrefix(authorization, bearer)
	require.True(t, ok, "access_tokens must be called with a Bearer JWT")

	parts := strings.Split(jwt, ".")
	require.Len(t, parts, 3)

	var header struct{ Alg string }
	decodeJWTPart(t, parts[0], &header)
	assert.Equal(t, "RS256", header.Alg)

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	require.NoError(t, err)

	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	require.NoError(t, rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig), "JWT signature must verify")

	var claims struct{ Iss, Iat, Exp int64 }
	decodeJWTPart(t, parts[1], &claims)

	now := clocktest.Instant().Unix()

	assert.Equal(t, testAppID, claims.Iss)
	assert.InDelta(t, now-60, claims.Iat, 5, "iat should be backdated ~60s")
	assert.Greater(t, claims.Exp, now)
	assert.LessOrEqual(t, claims.Exp, now+10*60, "GitHub rejects JWTs expiring more than 10 minutes ahead")
}

func decodeJWTPart(t *testing.T, part string, v any) {
	t.Helper()

	b, err := base64.RawURLEncoding.DecodeString(part)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, v))
}

func TestGitHubAppAuth_Success(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		configure    func(t *testing.T, r *githubretriever.Retriever)
		wantTokenURL string
	}{
		{
			name:         "private key",
			configure:    func(*testing.T, *githubretriever.Retriever) {},
			wantTokenURL: defaultTokenURL,
		},
		{
			name: "private key file",
			configure: func(t *testing.T, r *githubretriever.Retriever) {
				t.Helper()
				r.PrivateKeyFile = filepath.Join(t.TempDir(), "key.pem")
				require.NoError(t, os.WriteFile(r.PrivateKeyFile, []byte(r.PrivateKey), 0o600))
				r.PrivateKey = ""
			},
			wantTokenURL: defaultTokenURL,
		},
		{
			name: "GitHub Enterprise base URL",
			configure: func(_ *testing.T, r *githubretriever.Retriever) {
				r.BaseURL = "https://github.enterprise.example.com/api/v3"
			},
			wantTokenURL: "https://github.enterprise.example.com/api/v3/app/installations/67890/access_tokens",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			key, pemStr := generateTestKey(t)
			mock := &appMockHTTP{}
			r := newAppRetriever(pemStr, mock)
			tt.configure(t, r)
			require.NoError(t, r.Init(context.Background(), nil))

			got, err := r.Retrieve(context.Background())
			require.NoError(t, err)
			assert.Equal(t, []byte(flagBody), got)

			// Exactly 2 requests: access_tokens + contents
			require.Len(t, mock.recordedReqs, 2)
			tokenReq, contentsReq := mock.recordedReqs[0], mock.recordedReqs[1]

			assert.Equal(t, http.MethodPost, tokenReq.Method)
			assert.Equal(t, tt.wantTokenURL, tokenReq.URL.String())
			assert.Equal(t, "application/vnd.github+json", tokenReq.Header.Get("Accept"))
			assertAppJWT(t, tokenReq.Header.Get(authorization2), &key.PublicKey)

			// Contents request must carry the installation token as a Bearer token
			assert.Equal(t, http.MethodGet, contentsReq.Method)
			assert.Contains(t, contentsReq.URL.Path, "/repos/myorg/myrepo/contents/flags/config.yaml")
			assert.Equal(t, "Bearer ghs_token_1", contentsReq.Header.Get(authorization2))
		})
	}
}

func TestGitHubAppAuth_TokenRefresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		wantLastToken string
		tokenTTL      time.Duration
		wantMints     int
	}{
		{
			name:          "token still valid is reused",
			tokenTTL:      time.Hour,
			wantMints:     1,
			wantLastToken: "Bearer ghs_token_1",
		},
		{
			name:          "token inside the 5 minute refresh margin is re-minted",
			tokenTTL:      2 * time.Minute,
			wantMints:     2,
			wantLastToken: "Bearer ghs_token_2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, pemStr := generateTestKey(t)
			mock := &appMockHTTP{tokenTTL: tt.tokenTTL}
			r := newAppRetriever(pemStr, mock)

			_, err := r.Retrieve(context.Background())
			require.NoError(t, err)
			_, err = r.Retrieve(context.Background())
			require.NoError(t, err)

			assert.Equal(t, tt.wantMints, mock.tokenCalls)
			lastReq := mock.recordedReqs[len(mock.recordedReqs)-1]
			assert.Equal(t, tt.wantLastToken, lastReq.Header.Get(authorization2))
		})
	}
}

func TestGitHubAppAuth_401TriggersRetry(t *testing.T) {
	t.Parallel()

	_, pemStr := generateTestKey(t)
	mock := &appMockHTTP{first401ThenOK: true}
	r := newAppRetriever(pemStr, mock)

	got, err := r.Retrieve(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []byte(flagBody), got)

	// Requests: access_tokens (first mint) → contents (401) → access_tokens (re-mint) → contents (200)
	require.Len(t, mock.recordedReqs, 4)
	assert.Equal(t, "Bearer ghs_token_2", mock.recordedReqs[3].Header.Get(authorization2))
}

func TestGitHubAppAuth_ConcurrentRetrieveMintsOnce(t *testing.T) {
	t.Parallel()

	_, pemStr := generateTestKey(t)
	mock := &appMockHTTP{}
	r := newAppRetriever(pemStr, mock)

	const workers = 10

	var wg sync.WaitGroup

	errs := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			_, err := r.Retrieve(context.Background())
			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	assert.Equal(t, 1, mock.tokenCalls, "installation token should be minted only once")
	assert.Equal(t, workers, mock.contentsCalls)
}

func TestGitHubAppAuth_Errors(t *testing.T) {
	t.Parallel()

	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	ecDER, err := x509.MarshalECPrivateKey(ecKey)
	require.NoError(t, err)
	// The PEM decodes but x509.ParsePKCS1PrivateKey fails on the non-RSA bytes.
	wrongTypePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: ecDER}))

	tests := []struct {
		name      string
		mock      *appMockHTTP
		configure func(r *githubretriever.Retriever)
		wantErr   string
	}{
		{
			name: "token exchange failure surfaces GitHub message",
			mock: &appMockHTTP{
				tokenStatus: http.StatusUnauthorized,
				tokenBody:   []byte(`{"message":"A JSON web token could not be decoded"}`),
			},
			wantErr: "returned 401 for installation 67890: " +
				`{"message":"A JSON web token could not be decoded"}`,
		},
		{
			name:    "malformed token response",
			mock:    &appMockHTTP{tokenBody: []byte("not-valid-json")},
			wantErr: "failed to parse token response",
		},
		{
			name:    "empty token in response",
			mock:    &appMockHTTP{tokenBody: tokenResponse("", clocktest.Instant().Add(time.Hour))},
			wantErr: "token response contained no token",
		},
		{
			name:    "missing expires_at in response",
			mock:    &appMockHTTP{tokenBody: []byte(`{"token":"ghs_token"}`)},
			wantErr: "token response contained no expires_at",
		},
		{
			name:    "transport error on the retry after a 401",
			mock:    &appMockHTTP{first401ThenOK: true, retryErr: errConnectionReset},
			wantErr: "connection reset",
		},
		{
			name:      invalidBaseURL,
			configure: func(r *githubretriever.Retriever) { r.BaseURL = ":invalid:url" },
			wantErr:   invalidBaseURL,
		},
		{
			name:      "private key is not PEM",
			configure: func(r *githubretriever.Retriever) { r.PrivateKey = "not-a-pem-key" },
			wantErr:   "PEM",
		},
		{
			name:      "private key is not RSA",
			configure: func(r *githubretriever.Retriever) { r.PrivateKey = wrongTypePEM },
			wantErr:   "failed to parse RSA private key",
		},
		{
			name: "private key file missing",
			configure: func(r *githubretriever.Retriever) {
				r.PrivateKey = ""
				r.PrivateKeyFile = "/nonexistent/path/key.pem"
			},
			wantErr: "failed to read private key file",
		},
	}

	_, pemStr := generateTestKey(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newAppRetriever(pemStr, cmp.Or(tt.mock, &appMockHTTP{}))
			if tt.configure != nil {
				tt.configure(r)
			}

			_, retrieveErr := r.Retrieve(context.Background())
			require.Error(t, retrieveErr)
			assert.Contains(t, retrieveErr.Error(), tt.wantErr)
		})
	}
}

func TestGitHubAppAuth_Init(t *testing.T) {
	t.Parallel()

	_, pemStr := generateTestKey(t)

	tests := []struct {
		name      string
		retriever *githubretriever.Retriever
		wantErr   string
	}{
		{
			name:      "no auth",
			retriever: &githubretriever.Retriever{},
		},
		{
			name:      "token auth",
			retriever: &githubretriever.Retriever{GithubToken: ghpToken},
		},
		{
			name:      "app auth",
			retriever: newAppRetriever(pemStr, nil),
		},
		{
			name:      "app ID only",
			retriever: &githubretriever.Retriever{AppID: testAppID},
			wantErr:   areAllRequired,
		},
		{
			name:      "missing installation ID",
			retriever: &githubretriever.Retriever{AppID: testAppID, PrivateKey: pemStr},
			wantErr:   areAllRequired,
		},
		{
			name:      "missing private key",
			retriever: &githubretriever.Retriever{AppID: testAppID, InstallationID: testInstallationID},
			wantErr:   areAllRequired,
		},
		{
			name: "token and app fields",
			retriever: &githubretriever.Retriever{
				GithubToken:    ghpToken,
				AppID:          testAppID,
				InstallationID: testInstallationID,
				PrivateKey:     pemStr,
			},
			wantErr: "mutually exclusive",
		},
		{
			name: "invalid private key",
			retriever: &githubretriever.Retriever{
				AppID:          testAppID,
				InstallationID: testInstallationID,
				PrivateKey:     "not-a-pem-key",
			},
			wantErr: "PEM",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := tt.retriever
			assert.Equal(t, retriever.RetrieverNotReady, r.Status())

			err := r.Init(context.Background(), nil)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Equal(t, retriever.RetrieverError, r.Status())

				return
			}

			require.NoError(t, err)
			assert.Equal(t, retriever.RetrieverReady, r.Status())

			require.NoError(t, r.Shutdown(context.Background()))
			assert.Equal(t, retriever.RetrieverNotReady, r.Status())
		})
	}
}
