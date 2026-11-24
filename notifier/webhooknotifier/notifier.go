package webhooknotifier

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"

	"github.com/blackbirdworks/semaphore/internal"
	"github.com/blackbirdworks/semaphore/internal/signer"
	"github.com/blackbirdworks/semaphore/notifier"
)

var (
	errInvalidNotifierConfigurationNo = errors.New(
		"invalid notifier configuration, no endpointURL provided for the webhook notifier",
	)
	errErrorWhileCallingWebhook = errors.New("error: while calling webhook, statusCode")
)

var _ notifier.Notifier = &Notifier{}

// webhookReqBody is the format we are sending to the webhook.
type webhookReqBody struct {
	Meta  map[string]string  `json:"meta"`
	Flags notifier.DiffCache `json:"flags"`
}

// Notifier will call your endpoint URL with a POST request with the following format
//
//	{
//	  "meta": {
//	    "hostname": "server01"
//	  },
//	  "flags": {
//	    "deleted": {
//	      "test-flag": {
//	        "rule": "key eq \"random-key\"",
//	        "percentage": 100,
//	        "true": true,
//	        "false": false,
//	        "default": false
//	      }
//	    },
//	    "added": {
//	      "test-flag3": {
//	        "percentage": 5,
//	        "true": "test",
//	        "false": "false",
//	        "default": "default"
//	      }
//	    },
//	    "updated": {
//	      "test-flag2": {
//	        "old_value": {
//	          "rule": "key eq \"not-a-key\"",
//	          "percentage": 100,
//	          "true": true,
//	          "false": false,
//	          "default": false
//	        },
//	        "new_value": {
//	          "disable": true,
//	          "rule": "key eq \"not-a-key\"",
//	          "percentage": 100,
//	          "true": true,
//	          "false": false,
//	          "default": false
//	        }
//	      }
//	    }
//	  }
//	}
type Notifier struct {
	httpClient internal.HTTPClient
	// Meta (optional) information that you want to send to your webhook
	Meta map[string]string
	// Headers (optional) the list of Headers to send to the endpoint
	Headers map[string][]string
	// EndpointURL of your webhook
	EndpointURL string
	// Optional: Secret used to sign your request body.
	Secret string
	init   sync.Once
}

// Notify is the notifying all the changes to the notifier.
func (c *Notifier) Notify(diff notifier.DiffCache) error {
	if c.EndpointURL == "" {
		return errInvalidNotifierConfigurationNo
	}

	// init the notifier
	c.init.Do(func() {
		if c.httpClient == nil {
			c.httpClient = internal.DefaultHTTPClient()
		}

		if c.Meta == nil {
			c.Meta = make(map[string]string)
		}

		// if no hostname provided we return the hostname of the current machine
		if _, ok := c.Meta["hostname"]; !ok {
			hostname, _ := os.Hostname()
			c.Meta["hostname"] = hostname
		}
	})

	endpointURL, err := url.Parse(c.EndpointURL)
	if err != nil {
		// the endpoint URL may embed credentials, it must not be part of the error.
		return fmt.Errorf("error: (Webhook Notifier) invalid EnpointURL: %w", internal.RedactURLError(err))
	}

	// Create request body
	reqBody := webhookReqBody{
		Meta:  c.Meta,
		Flags: diff,
	}

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("error: (Webhook Notifier) impossible to read differences; %w", err)
	}

	// Notifications can overlap, so each request needs its own headers and signature.
	headers := http.Header(c.Headers).Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Content-Type", "application/json")

	// if a secret is provided we sign the body and add this signature as a header.
	if c.Secret != "" {
		headers.Set("X-Hub-Signature-256", signer.Sign(payload, []byte(c.Secret)))
	}

	request := http.Request{
		Method: http.MethodPost,
		URL:    endpointURL,
		Header: headers,
		Body:   io.NopCloser(bytes.NewReader(payload)),
	}
	response, err := c.httpClient.Do(&request)
	// Log if something went wrong while calling the webhook.
	if err != nil {
		return fmt.Errorf("error: while calling webhook: %w", internal.RedactURLError(err))
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%w = %d", errErrorWhileCallingWebhook, response.StatusCode)
	}

	return nil
}
