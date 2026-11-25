package config

import (
	"errors"
	"fmt"
)

var (
	errInvalidNotifierNoSlackWebhookURL = errors.New("invalid notifier: no \"slackWebhookUrl\" property found for kind")
	errInvalidNotifierNoWebhookURL      = errors.New("invalid notifier: no \"WebhookURL\" property found for kind")
	errInvalidNotifierNoEndpointURL     = errors.New("invalid notifier: no \"endpointUrl\" property found for kind")
	errInvalidNotifierNoLowerWebhookURL = errors.New("invalid notifier: no \"webhookUrl\" property found for kind")
	errInvalidNotifierKind              = errors.New("invalid notifier: kind")
)

type NotifierConf struct {
	Kind NotifierKind `koanf:"kind" mapstructure:"kind"`
	// Deprecated: Use WebhookURL instead
	SlackWebhookURL string              `koanf:"slackwebhookurl" mapstructure:"slackWebhookUrl"`
	EndpointURL     string              `koanf:"endpointurl"     mapstructure:"endpointUrl"`
	Secret          string              `koanf:"secret"          mapstructure:"secret"`
	Meta            map[string]string   `koanf:"meta"            mapstructure:"meta"`
	Headers         map[string][]string `koanf:"headers"         mapstructure:"headers"`
	WebhookURL      string              `koanf:"webhookurl"      mapstructure:"webhookUrl"`
}

func (c *NotifierConf) IsValid() error {
	if err := c.Kind.IsValid(); err != nil {
		return err
	}
	if c.Kind == SlackNotifier && (c.SlackWebhookURL == "" && c.WebhookURL == "") {
		return fmt.Errorf("%w \"%s\"", errInvalidNotifierNoSlackWebhookURL,
			c.Kind,
		)
	}
	if c.Kind == MicrosoftTeamsNotifier && c.WebhookURL == "" {
		return fmt.Errorf("%w \"%s\"", errInvalidNotifierNoWebhookURL,
			c.Kind,
		)
	}
	if c.Kind == WebhookNotifier && c.EndpointURL == "" {
		return fmt.Errorf("%w \"%s\"", errInvalidNotifierNoEndpointURL,
			c.Kind,
		)
	}
	if c.Kind == DiscordNotifier && c.WebhookURL == "" {
		return fmt.Errorf("%w \"%s\"", errInvalidNotifierNoLowerWebhookURL,
			c.Kind,
		)
	}

	return nil
}

type NotifierKind string

const (
	SlackNotifier          NotifierKind = "slack"
	MicrosoftTeamsNotifier NotifierKind = "microsoftteams"
	WebhookNotifier        NotifierKind = "webhook"
	DiscordNotifier        NotifierKind = "discord"
)

// IsValid is checking if the value is part of the enum.
func (r NotifierKind) IsValid() error {
	switch r {
	case SlackNotifier, WebhookNotifier, DiscordNotifier, MicrosoftTeamsNotifier:
		return nil
	}

	return fmt.Errorf("%w \"%s\" is not supported", errInvalidNotifierKind, r)
}
