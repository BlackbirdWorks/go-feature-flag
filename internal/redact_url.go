package internal

import (
	"errors"
	"net/url"
)

// RedactedURL replaces the URL of an error that must not leak it.
const RedactedURL = "[REDACTED]"

// RedactURLError removes the URL from the errors returned by net/http and net/url.
//
// Those errors embed the full request URL, but the URL of a webhook (Slack, Discord, ...) or of an
// endpoint may carry a secret (token in the path, credentials in the user info or in the query string).
// The operation and the root cause are kept so the error stays actionable.
func RedactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return &url.Error{Op: urlErr.Op, URL: RedactedURL, Err: urlErr.Err}
	}
	return err
}
