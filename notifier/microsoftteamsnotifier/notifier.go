package microsoftteamsnotifier

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	goteamsnotify "github.com/atc0005/go-teams-notify/v2"
	"github.com/atc0005/go-teams-notify/v2/adaptivecard"

	"github.com/blackbirdworks/semaphore/notifier"
)

var errNoWebhookURL = errors.New("error: (Microsoft Teams Notifier) invalid notifier configuration, no " +
	"MicrosoftTeamsWebhookURL provided for the microsoft teams notifier")

var _ notifier.Notifier = &Notifier{}

// Notifier is the component in charge of sending flag changes to Microsoft Teams.
type Notifier struct {
	teamsClient              *goteamsnotify.TeamsClient
	MicrosoftTeamsWebhookURL string
}

// Notify is the notifying all the changes to the notifier.
func (c *Notifier) Notify(diff notifier.DiffCache) error {
	if c.MicrosoftTeamsWebhookURL == "" {
		return errNoWebhookURL
	}

	msgText := convertToMicrosoftTeamsMessage(diff)

	msg, err := adaptivecard.NewSimpleMessage(msgText, "Semaphore", true)
	if err != nil {
		return fmt.Errorf("error: (Microsoft Teams Notifier) create adaptive card message: %w", err)
	}

	if c.teamsClient == nil {
		c.teamsClient = goteamsnotify.NewTeamsClient()
	}

	err = c.teamsClient.Send(c.MicrosoftTeamsWebhookURL, msg)
	if err != nil {
		return fmt.Errorf("error: (Microsoft Teams Notifier) send webhook: %w", err)
	}

	return nil
}

func convertToMicrosoftTeamsMessage(diffCache notifier.DiffCache) string {
	hostname, _ := os.Hostname()

	var msgText strings.Builder
	fmt.Fprintf(&msgText, "Changes detected in your feature flag file on: **%s**", hostname)

	// Deleted
	deletedKeys := make([]string, 0, len(diffCache.Deleted))
	for k := range diffCache.Deleted {
		deletedKeys = append(deletedKeys, k)
	}

	slices.Sort(deletedKeys)

	for _, key := range deletedKeys {
		fmt.Fprintf(&msgText, "\n * ❌ Flag **%s** deleted", key)
	}

	// Added
	addedKeys := make([]string, 0, len(diffCache.Added))
	for k := range diffCache.Added {
		addedKeys = append(addedKeys, k)
	}

	slices.Sort(addedKeys)

	for _, key := range addedKeys {
		fmt.Fprintf(&msgText, "\n * 🆕 Flag **%s** created%s", key, notifier.UpdatedBySuffix(diffCache.Added[key]))
	}

	// Updated
	updatedKeys := make([]string, 0, len(diffCache.Updated))
	for k := range diffCache.Updated {
		updatedKeys = append(updatedKeys, k)
	}

	slices.Sort(updatedKeys)

	for _, key := range updatedKeys {
		value := diffCache.Updated[key]
		fmt.Fprintf(&msgText, "\n * ✏️ Flag **%s** updated%s", key, notifier.UpdatedBySuffix(value.After))
		// Adaptive Card markdown has no inline code, so the changes are described without it.
		changes, err := notifier.DescribeChangesPlain(value.Before, value.After)
		if err != nil {
			fmt.Fprintf(&msgText, "\n   * %s: %s", notifier.ChangesUnavailable, err)

			continue
		}

		if len(changes) > 0 {
			msgText.WriteString("\n" + notifier.ChangeList(changes, "   * ", 0))
		}
	}

	return msgText.String()
}
