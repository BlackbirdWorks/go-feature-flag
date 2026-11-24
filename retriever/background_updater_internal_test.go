package retriever

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestClose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		backgroundUpdater backgroundUpdater
		name              string
		// wantSignalClosed is true when closing must end the wait of the goroutines that read updaterChan.
		wantSignalClosed bool
	}{
		{
			name: "Close nil background updater",
			backgroundUpdater: backgroundUpdater{
				ticker:      nil,
				updaterChan: nil,
			},
		},
		{
			name:              "Close new background updater",
			backgroundUpdater: newBackgroundUpdater(500*time.Millisecond, false),
			wantSignalClosed:  true,
		},
		{
			name:              "Close new background updater with jitter",
			backgroundUpdater: newBackgroundUpdater(500*time.Millisecond, true),
			wantSignalClosed:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.NotPanics(t, func() {
				tt.backgroundUpdater.close()
			})

			closed := false
			if tt.backgroundUpdater.updaterChan != nil {
				select {
				case <-tt.backgroundUpdater.updaterChan:
					closed = true
				default:
				}
			}
			assert.Equal(t, tt.wantSignalClosed, closed, "updaterChan is closed")
		})
	}
}
