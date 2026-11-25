package proxynotifier_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/proxynotifier"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/modules/core/flag"
	"github.com/blackbirdworks/semaphore/modules/core/testhelpers/clocktest"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	myFlagset = "my-flagset"
)

func TestNotifierSSE_Notify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		flagsetName string
		diff        notifier.DiffCache
		wantEvent   model.OFREPSSEEvent
	}{
		{
			name:        "empty diff refetches the evaluations",
			flagsetName: myFlagset,
			diff:        notifier.DiffCache{},
			wantEvent: model.OFREPSSEEvent{
				Type:         model.OFREPSSEEventTypeRefetchEvaluation,
				LastModified: clocktest.Instant().Unix(),
			},
		},
		{
			name:        "diff with added flag refetches the evaluations, the diff is not sent",
			flagsetName: myFlagset,
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
			wantEvent: model.OFREPSSEEvent{
				Type:         model.OFREPSSEEventTypeRefetchEvaluation,
				LastModified: clocktest.Instant().Unix(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			sseService := stream.NewSSEService()
			defer sseService.Close()

			srv := httptest.NewServer(http.HandlerFunc(sseService.ServeHTTP))
			defer srv.Close()

			subscribed := make(chan struct{}, 1)

			sseService.SetOnSubscribe(func(string) {
				select {
				case subscribed <- struct{}{}:
				default:
				}
			})

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?stream="+tt.flagsetName, http.NoBody)
			require.NoError(t, err)
			resp, err := (&http.Client{}).Do(req)
			require.NoError(t, err)

			defer func() { _ = resp.Body.Close() }()

			select {
			case <-subscribed:
			case <-ctx.Done():
				require.Fail(t, "timed out waiting for the SSE client to subscribe")
			}

			require.NoError(t, proxynotifier.NewNotifierSSE(sseService, tt.flagsetName).Notify(tt.diff))

			fields := map[string]string{}

			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				line := scanner.Text()
				if strings.HasPrefix(line, ":") {
					continue
				}

				if line == "" && len(fields) > 0 {
					break
				}

				if key, value, ok := strings.Cut(line, ": "); ok {
					fields[key] = value
				}
			}

			var got model.OFREPSSEEvent
			require.NoError(t, json.Unmarshal([]byte(fields["data"]), &got))

			assert.Equal(t, "message", fields["event"])
			assert.Equal(t, strconv.FormatInt(clocktest.Instant().UnixNano(), 10), fields["id"])
			assert.Equal(t, tt.wantEvent, got)
		})
	}
}
