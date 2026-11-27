package relay

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func newJSONRequest(url, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create JSON HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", APIKey)

	return req, nil
}
