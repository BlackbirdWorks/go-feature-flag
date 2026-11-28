package semaphore_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const betaFlag = "beta"

const segmentStatusFlags = `
beta:
  variations:
    on: true
    off: false
  targeting:
    - name: members
      query: targetingKey in segment("beta")
      variation: on
  defaultRule:
    variation: off
plain:
  variations:
    on: true
    off: false
  defaultRule:
    variation: on
`

var errStoreDown = errors.New("the store is down")

type downProvider struct{}

func (downProvider) Contains(context.Context, []segment.Lookup) error { return errStoreDown }

// The v1 evaluation and all flags responses carry semaphore_segmentStatus for an evaluation whose segment lookups
// were not healthy, and nothing for a flag without a segment.
func TestSegmentStatusInTheV1Responses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider   segment.Provider
		name       string
		flag       string
		wantStatus string
	}{
		{
			name:       "store error",
			flag:       betaFlag,
			provider:   downProvider{},
			wantStatus: `"semaphore_segmentStatus":"STORE_ERROR"`,
		},
		{name: "a flag without a segment", flag: "plain", provider: downProvider{}},
		{name: "healthy", flag: betaFlag, provider: segment.NewMemoryProvider(map[string][]string{"beta": {"user"}})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "flags.yaml")
			require.NoError(t, os.WriteFile(path, []byte(segmentStatusFlags), 0o600))

			cfg := mock.FileConfig(path)
			cfg.SegmentProvider = tt.provider
			manager := mock.NewDefault(t, &cfg)
			body := `{"evaluationContext": {"key": "user"}, "defaultValue": false}`

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/v1/feature/"+tt.flag+"/eval", strings.NewReader(body))
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			req.SetPathValue("flagKey", tt.flag)
			require.NoError(t, controller.NewFlagEval(manager, &metric.Metrics{}).Handler(rec, req))
			assertV1Status(t, tt.wantStatus, rec.Body.String())

			all := httptest.NewRecorder()
			allReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/allflags",
				strings.NewReader(`{"evaluationContext": {"key": "user"}}`))
			allReq.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			require.NoError(t, controller.NewAllFlags(manager, &metric.Metrics{}).Handler(all, allReq))

			if tt.flag == betaFlag {
				assertV1Status(t, tt.wantStatus, all.Body.String())
			}
		})
	}
}

func assertV1Status(t *testing.T, want, body string) {
	t.Helper()

	if want == "" {
		assert.NotContains(t, body, "semaphore_segmentStatus")

		return
	}

	assert.Contains(t, body, want)
}
