package events_test

import "time"

// Names the tests share.
const (
	// clockStep is the step of the clock of the package (see TestMain); stepSeconds is the same in seconds.
	clockStep   = time.Second
	stepSeconds = 1.0

	nameBad       = "bad"
	kindNope      = "nope"
	connSQS       = "sqsc"
	nameFiles     = "files"
	envURL        = "URL"
	envSecret     = "SECRET"
	eventOneKey   = "flag-1"
	eventTwoKey   = "flag-2"
	eventThreeKey = "flag-3"
	headerValue   = "header-value"
	secretValue   = "signing"
	hookURL       = "https://hook.example/x"
	kindWebhook   = "webhook"

	labelExporter = "exporter"
	labelNotifier = "notifier"
	labelState    = "state"
)
