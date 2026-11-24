package internal

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// RedactedURL replaces the URL of an error that must not leak it.
const RedactedURL = "[REDACTED]"

var embeddedURL = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.\-]*://[^\s"']*`)

// RedactURLError removes the URLs from the errors returned by net/http and net/url.
//
// Those errors embed the full request URL, but the URL of a webhook (Slack, Discord, ...) or of an
// endpoint may carry a secret (token in the path, credentials in the user info or in the query string).
// The operation and the root cause are kept so the error stays actionable. When the cause itself quotes a
// URL (for example a malformed redirect Location header), the cause is rewritten without it. The cause of a
// parse error is always replaced, because it quotes the unparsable input.
func RedactURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	cause := urlErr.Err
	if urlErr.Op == "parse" {
		// The causes of url.Parse quote a part of the raw input (for example `invalid port ":secret" after
		// host` when the password of "https://user:secret" is read as a port), and the input can not be
		// trusted to be free of credentials. The cause is dropped instead of being pattern matched.
		return &url.Error{Op: urlErr.Op, URL: RedactedURL, Err: errors.New("invalid URL")}
	}
	if cause != nil {
		msg := cause.Error()
		switch {
		case strings.Contains(msg, "Location header"):
			// the redirect target is quoted, possibly without a scheme.
			cause = errors.New("invalid Location header in the redirect response")
		case embeddedURL.MatchString(msg):
			cause = errors.New(embeddedURL.ReplaceAllString(msg, RedactedURL))
		}
	}

	return &url.Error{Op: urlErr.Op, URL: RedactedURL, Err: cause}
}

// RedactURL returns a URL that is safe to put in an error or a log: only the scheme and the host are kept.
// The user info, the path (tokens are sometimes carried by it), the query string and the fragment may carry
// credentials, so they are dropped.
// A value that can not be parsed is replaced entirely, because its content is unknown.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return RedactedURL
	}

	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}
