package proxynotifier_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/proxynotifier"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/notifier"
)

func TestNotifierSSE_Notify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flagsetName string
		diff        notifier.DiffCache
	}{
		{
			name:        "empty diff notifies without error",
			flagsetName: "my-flagset",
			diff:        notifier.DiffCache{},
		},
		{
			name:        "diff with added flag notifies without error",
			flagsetName: "my-flagset",
			diff: notifier.DiffCache{
				Added: map[string]flag.Flag{
					"flag-1": &flag.InternalFlag{
						Variations: &map[string]*any{
							"A": new(any(true)),
						},
						DefaultRule: &flag.Rule{VariationResult: new("A")},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sseService := stream.NewSSEService()
			defer sseService.Close()

			n := proxynotifier.NewNotifierSSE(sseService, tt.flagsetName)
			assert.NoError(t, n.Notify(tt.diff))
		})
	}
}
