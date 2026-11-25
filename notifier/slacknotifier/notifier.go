package slacknotifier

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/blackbirdworks/semaphore/internal"
	"github.com/blackbirdworks/semaphore/notifier"
)

var errNoWebhookURL = errors.New("error: (Slack Notifier) invalid notifier configuration, no " +
	"SlackWebhookURL provided for the slack notifier")

var (
	errErrorSlackNotifierWhile = errors.New("error: (Slack Notifier) while calling slack webhook, statusCode")
)

var _ notifier.Notifier = &Notifier{}

const (
	slackFooter  = "semaphore"
	colorDeleted = "#FF0000"
	colorUpdated = "#FFA500"
	colorAdded   = "#008000"
)

// escapeSlack escapes the characters Slack reserves for links and mentions, so flag values
// such as "<!channel>" are shown as typed. See https://api.slack.com/reference/surfaces/formatting#escaping
func escapeSlack(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Notifier is the component in charge of sending flag changes to Slack.
type Notifier struct {
	httpClient      internal.HTTPClient
	SlackWebhookURL string
	init            sync.Once
}

// Notify is the notifying all the changes to the notifier.
func (c *Notifier) Notify(diff notifier.DiffCache) error {
	if c.SlackWebhookURL == "" {
		return errNoWebhookURL
	}

	// init the notifier
	c.init.Do(func() {
		if c.httpClient == nil {
			c.httpClient = internal.DefaultHTTPClient()
		}
	})

	slackURL, err := url.Parse(c.SlackWebhookURL)
	if err != nil {
		// the webhook URL is the secret of a Slack webhook, it must not be part of the error.
		return fmt.Errorf("error: (Slack Notifier) invalid SlackWebhookURL: %w", internal.RedactURLError(err))
	}

	reqBody := convertToSlackMessage(diff)

	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("error: (Slack Notifier) impossible to read differences; %w", err)
	}

	request := http.Request{
		Method: http.MethodPost,
		URL:    slackURL,
		Body:   io.NopCloser(bytes.NewReader(payload)),
		Header: map[string][]string{"Content-type": {"application/json"}},
	}

	response, err := c.httpClient.Do(&request)
	if err != nil {
		return fmt.Errorf("error: (Slack Notifier) error: while calling webhook: %w", internal.RedactURLError(err))
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("%w = %d", errErrorSlackNotifierWhile,
			response.StatusCode)
	}

	return nil
}

func convertToSlackMessage(diffCache notifier.DiffCache) slackMessage {
	hostname, _ := os.Hostname()
	attachments := convertDeletedFlagsToSlackMessage(diffCache)
	attachments = append(attachments, convertUpdatedFlagsToSlackMessage(diffCache)...)
	attachments = append(attachments, convertAddedFlagsToSlackMessage(diffCache)...)
	res := slackMessage{
		Text:        fmt.Sprintf("Changes detected in your feature flag file on: *%s*", hostname),
		Attachments: attachments,
	}

	return res
}

func convertDeletedFlagsToSlackMessage(diffCache notifier.DiffCache) []attachment {
	attachments := make([]attachment, 0, len(diffCache.Deleted))
	for key := range diffCache.Deleted {
		attachment := attachment{
			Title:  fmt.Sprintf("❌ Flag %q deleted", key),
			Color:  colorDeleted,
			Footer: slackFooter,
		}
		attachments = append(attachments, attachment)
	}

	return attachments
}

func convertUpdatedFlagsToSlackMessage(diffCache notifier.DiffCache) []attachment {
	attachments := make([]attachment, 0, len(diffCache.Updated))
	for key, value := range diffCache.Updated {
		attachment := attachment{
			Title:    fmt.Sprintf("✏️ Flag %q updated%s", key, notifier.UpdatedBySuffix(value.After)),
			Color:    colorUpdated,
			Footer:   slackFooter,
			MrkdwnIn: []string{"text"},
		}

		changes, err := notifier.DescribeChanges(value.Before, value.After)
		if err != nil {
			attachment.Text = fmt.Sprintf("%s: %s", notifier.ChangesUnavailable, err.Error())
		} else {
			attachment.Text = escapeSlack(notifier.ChangeList(changes, "• ", 0))
		}

		attachments = append(attachments, attachment)
	}

	return attachments
}

func convertAddedFlagsToSlackMessage(diff notifier.DiffCache) []attachment {
	attachments := make([]attachment, 0, len(diff.Added))
	for key, value := range diff.Added {
		attachment := attachment{
			Title:  fmt.Sprintf("🆕 Flag %q created%s", key, notifier.UpdatedBySuffix(value)),
			Color:  colorAdded,
			Footer: slackFooter,
		}
		attachments = append(attachments, attachment)
	}

	return attachments
}

type slackMessage struct {
	IconURL     string       `json:"icon_url,omitempty"` //nolint:tagliatelle // Slack incoming webhook API
	Text        string       `json:"text"`
	Attachments []attachment `json:"attachments"`
}

type attachment struct {
	Color      string   `json:"color"`
	Title      string   `json:"title"`
	Text       string   `json:"text,omitempty"`
	FooterIcon string   `json:"footer_icon,omitempty"` //nolint:tagliatelle // Slack incoming webhook API
	Footer     string   `json:"footer,omitempty"`
	MrkdwnIn   []string `json:"mrkdwn_in,omitempty"` //nolint:tagliatelle // Slack incoming webhook API
	Fields     []Field  `json:"fields"`
}

// Field is the representation of a field in a slack message.
type Field struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}
