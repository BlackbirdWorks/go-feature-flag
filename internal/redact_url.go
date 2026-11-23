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
// URL (for example a malformed redirect Location header), the cause is rewritten without it.
func RedactURLError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	cause := urlErr.Err
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
