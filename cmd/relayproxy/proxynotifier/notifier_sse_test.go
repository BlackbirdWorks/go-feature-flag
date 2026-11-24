package proxynotifier_test

import (
	"testing"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/proxynotifier"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testutils/testconvert"
	"github.com/blackbirdworks/semaphore/notifier"
	"github.com/stretchr/testify/assert"
)

func TestNotifierSSE_Notify(t *testing.T) {
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
							"A": testconvert.Interface(true),
						},
						DefaultRule: &flag.Rule{VariationResult: testconvert.String("A")},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sseService := stream.NewSSEService()
			defer sseService.Close()

			n := proxynotifier.NewNotifierSSE(sseService, tt.flagsetName)
			assert.NoError(t, n.Notify(tt.diff))
		})
	}
}
