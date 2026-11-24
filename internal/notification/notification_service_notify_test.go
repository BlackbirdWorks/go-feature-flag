package notification_test

import (
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/thejerf/slogassert"

	"github.com/blackbirdworks/semaphore/internal/notification"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/blackbirdworks/semaphore/utils/fflog"
)

var (
	errBoom = errors.New("boom")
)

func TestNotificationServiceNotify(t *testing.T) {
	t.Parallel()
	yo := func() map[string]flag.Flag {
		return map[string]flag.Flag{"yo": &flag.InternalFlag{Version: new("1.0")}}
	}
	yoNew := map[string]flag.Flag{"yo-new": &flag.InternalFlag{Version: new("1.0")}}
	tests := []struct {
		notifierErr  error
		oldCache     map[string]flag.Flag
		newCache     map[string]flag.Flag
		name         string
		wantCalls    int32
		wantLogError bool
	}{
		{name: "a difference calls the notifier", oldCache: yo(), newCache: yoNew, wantCalls: 1},
		{name: "no difference does not call the notifier", oldCache: yo(), newCache: yo(), wantCalls: 0},
		{name: "a notifier error is logged", oldCache: yo(), newCache: yoNew, notifierErr: errBoom,
			wantCalls: 1, wantLogError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			handler := slogassert.New(t, slog.LevelDebug, nil)
			n := &notifierMock{err: tt.notifierErr}
			c := notification.NewService([]notifier.Notifier{n})

			c.Notify(tt.oldCache, tt.newCache, &fflog.FFLogger{LeveledLogger: slog.New(handler)})
			c.Close() // waits for the notifier goroutines

			assert.Equal(t, tt.wantCalls, n.calls.Load())
			if tt.wantLogError {
				handler.AssertMessage("error while calling the notifier")
			}
			handler.AssertEmpty()
		})
	}
}

type notifierMock struct {
	err   error
	calls atomic.Int32
}

func (n *notifierMock) Notify(notifier.DiffCache) error {
	n.calls.Add(1)

	return n.err
}
