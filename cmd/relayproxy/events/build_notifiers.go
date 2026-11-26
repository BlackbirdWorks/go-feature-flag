package events

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/bootstrap"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/notifier/discordnotifier"
	"github.com/blackbirdworks/semaphore/notifier/microsoftteamsnotifier"
	"github.com/blackbirdworks/semaphore/notifier/slacknotifier"
	"github.com/blackbirdworks/semaphore/notifier/webhooknotifier"
)

// builtNotifier is the notifier a builder made: a struct, so that the builders return a concrete type.
type builtNotifier struct {
	notifier.Notifier
}

// NotifierDef is a notifier built from its spec, not started yet.
type NotifierDef struct {
	// Notifier is the engine notifier.
	Notifier notifier.Notifier
	// Name is the name of the entry in the notifiers section.
	Name string
	// Kind is the kind of the notifier.
	Kind string
	// Identity changes when anything that affects the notifier changes, secrets included.
	Identity string
}

// Notifiers builds the notifiers of the specs, sorted by name. It returns every failing entry, each as an
// [EntryError], and no notifier when one failed.
func (b Builder) Notifiers(_ context.Context, specs Specs) ([]NotifierDef, error) {
	names := slices.Sorted(maps.Keys(specs.Notifiers))
	defs := make([]NotifierDef, 0, len(names))

	var errs []error

	for i, name := range names {
		def, err := b.notifier(name, new(specs.Notifiers[name]))
		if err != nil {
			errs = append(errs, &EntryError{Resource: "notifiers", Index: i, Name: name, Err: err})

			continue
		}

		defs = append(defs, def)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return defs, nil
}

func (b Builder) notifier(name string, spec *bootstrap.Notifier) (NotifierDef, error) {
	id, err := newIdentity(spec.Kind, *spec)
	if err != nil {
		return NotifierDef{}, err
	}

	var built builtNotifier

	switch spec.Kind {
	case bootstrap.NotifierSlack, bootstrap.NotifierMicrosoftTeams, bootstrap.NotifierDiscord:
		built, err = b.chatNotifier(id, spec)
	case bootstrap.NotifierWebhook:
		built, err = b.webhookNotifier(id, spec)
	default:
		return NotifierDef{}, fmt.Errorf("%w: %q", ErrUnknownKind, spec.Kind)
	}

	if err != nil {
		return NotifierDef{}, err
	}

	return NotifierDef{Name: name, Kind: spec.Kind, Identity: id.String(), Notifier: built.Notifier}, nil
}

func (b Builder) chatNotifier(id *identity, spec *bootstrap.Notifier) (builtNotifier, error) {
	if !spec.WebhookURL.IsSet() {
		return builtNotifier{}, settingsError("webhookUrl", "is required")
	}

	url, err := b.secret(id, "webhookUrl", spec.WebhookURL)
	if err != nil {
		return builtNotifier{}, err
	}

	switch spec.Kind {
	case bootstrap.NotifierSlack:
		return builtNotifier{Notifier: &slacknotifier.Notifier{SlackWebhookURL: url}}, nil
	case bootstrap.NotifierMicrosoftTeams:
		return builtNotifier{Notifier: &microsoftteamsnotifier.Notifier{MicrosoftTeamsWebhookURL: url}}, nil
	default: // bootstrap.NotifierDiscord
		return builtNotifier{Notifier: &discordnotifier.Notifier{DiscordWebhookURL: url}}, nil
	}
}

func (b Builder) webhookNotifier(id *identity, spec *bootstrap.Notifier) (builtNotifier, error) {
	if !spec.EndpointURL.IsSet() {
		return builtNotifier{}, settingsError("endpointUrl", "is required")
	}

	endpoint, err := b.secret(id, "endpointUrl", spec.EndpointURL)
	if err != nil {
		return builtNotifier{}, err
	}

	secret, err := b.secret(id, "secret", spec.Secret)
	if err != nil {
		return builtNotifier{}, err
	}

	headers, err := b.headers(id, spec.Headers)
	if err != nil {
		return builtNotifier{}, err
	}

	return builtNotifier{Notifier: &webhooknotifier.Notifier{
		EndpointURL: endpoint, Secret: secret, Meta: spec.Meta, Headers: headers,
	}}, nil
}
