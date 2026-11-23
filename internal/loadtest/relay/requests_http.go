package relay

import (
	"context"
	"net/http"
	"strings"
)

func newJSONRequest(url, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}
