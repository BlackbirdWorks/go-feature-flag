package githubretriever

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/modules/core/clock"
)

const (
	// jwtLifetime is how long a GitHub App JWT is valid (GitHub allows 10 minutes at most).
	jwtLifetime = 9 * time.Minute
	// jwtHeader is the static RS256 JWT header JSON.
	jwtHeader = `{"alg":"RS256","typ":"JWT"}`
)

// signAppJWT creates a minimal RS256 JWT for GitHub App authentication using
// stdlib only (no external JWT library). The token is valid for 9 minutes,
// which is within GitHub's 10-minute maximum.
//
//   - iss: appID
//   - iat: now − 60 s  (clock-skew tolerance)
//   - exp: now + 9 min
func signAppJWT(appID int64, key *rsa.PrivateKey) (string, error) {
	now := clock.Now()

	claimsJSON := fmt.Sprintf(
		`{"exp":%d,"iat":%d,"iss":%d}`,
		now.Add(jwtLifetime).Unix(),
		now.Add(-60*time.Second).Unix(),
		appID,
	)

	signingInput := jwtBase64([]byte(jwtHeader)) + "." + jwtBase64([]byte(claimsJSON))

	digest := sha256.Sum256([]byte(signingInput))
	// GitHub App JWTs require RS256 (PKCS#1 v1.5 + SHA-256); PSS/PS256 is not accepted by GitHub.
	// https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/
	// generating-a-json-web-token-jwt-for-a-github-app
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("github app jwt: failed to sign: %w", err)
	}

	return signingInput + "." + jwtBase64(sig), nil
}

// jwtBase64 is base64url encoding without padding, as required by RFC 7515.
func jwtBase64(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
