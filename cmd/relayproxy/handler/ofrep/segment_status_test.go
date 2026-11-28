package ofrep_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/modules/core/segment"
)

const (
	betaFlag   = "beta"
	statusKey  = "semaphore_segmentStatus"
	storeError = "STORE_ERROR"
)

const segmentFlags = `
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
own:
  variations:
    on: true
    off: false
  metadata:
    semaphore_segmentStatus: its-own
  defaultRule:
    variation: on
plain:
  variations:
    on: true
    off: false
  defaultRule:
    variation: on
`

var errSegmentStoreDown = errors.New("the store is down")

type downSegmentProvider struct{}

func (downSegmentProvider) Contains(context.Context, []segment.Lookup) error {
	return errSegmentStoreDown
}

// The evaluation responses carry semaphore_segmentStatus when an evaluation used a segment and the lookups were not
// healthy; an evaluation without a segment, and a healthy one, have the metadata of before.
func TestSegmentStatusInTheResponses(t *testing.T) {
	t.Parallel()

	// wantBeta is the status that the beta flag (the one with a segment) carries, in the single and in the bulk
	// response; the other flags never carry one of the relay.
	tests := []struct {
		provider segment.Provider
		name     string
		flag     string
		wantBeta string
		opts     []ofrep.Option
	}{
		{name: "store error", flag: betaFlag, provider: downSegmentProvider{}, wantBeta: storeError},
		{name: "not configured", flag: betaFlag, wantBeta: "NOT_CONFIGURED"},
		{
			name: "healthy is not reported", flag: betaFlag,
			provider: segment.NewMemoryProvider(map[string][]string{"beta": {"user"}}),
		},
		{name: "a flag without a segment", flag: "plain", provider: downSegmentProvider{}, wantBeta: storeError},
		{
			name:     "a flag with metadata of its own",
			flag:     "own",
			provider: downSegmentProvider{},
			wantBeta: storeError,
		},
		{
			name: "legacy metadata only", flag: betaFlag, provider: downSegmentProvider{},
			opts: []ofrep.Option{ofrep.WithLegacyMetadataOnly(true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "flags.yaml")
			require.NoError(t, os.WriteFile(path, []byte(segmentFlags), 0o600))

			cfg := mock.FileConfig(path)
			cfg.SegmentProvider = tt.provider
			manager := mock.NewDefault(t, &cfg)
			ctrl := ofrep.NewOFREPEvaluate(manager, &metric.Metrics{}, ofrep.EventStream{}, tt.opts...)
			body := `{"context": {"targetingKey": "user"}}`

			single := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/ofrep/v1/evaluate/flags/"+tt.flag, strings.NewReader(body))
			req.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

			mux := http.NewServeMux()
			mux.Handle(postOfrepVOneEvaluate, helper.Handle(ctrl.Evaluate, false))
			mux.ServeHTTP(single, req)
			require.Equal(t, http.StatusOK, single.Code, single.Body.String())

			var one model.OFREPEvaluateSuccessResponse
			require.NoError(t, json.Unmarshal(single.Body.Bytes(), &one))
			assertBulk(t, wantFor(tt.flag, tt.wantBeta), tt.flag, one.Metadata)

			bulk := httptest.NewRecorder()
			bulkReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/ofrep/v1/evaluate/flags", strings.NewReader(body))
			bulkReq.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			require.NoError(t, ctrl.BulkEvaluate(bulk, bulkReq))

			var all model.OFREPBulkEvaluateSuccessResponse
			require.NoError(t, json.Unmarshal(bulk.Body.Bytes(), &all))

			seen := map[string]bool{}

			for i := range all.Flags {
				seen[all.Flags[i].Key] = true
				assertBulk(t, wantFor(all.Flags[i].Key, tt.wantBeta), all.Flags[i].Key, all.Flags[i].Metadata)
			}

			assert.True(t, seen[betaFlag] && seen["plain"] && seen["own"], "the bulk response has every flag: %v", seen)
		})
	}
}

func assertStatus(t *testing.T, want string, metadata map[string]any, flag string) {
	t.Helper()

	if want == "" {
		assert.NotContains(t, metadata, statusKey, flag)

		return
	}

	assert.Equal(t, want, metadata[statusKey], flag)
}

// assertBulk checks the status of a flag of a response: the flag with metadata of its own keeps its value.
func assertBulk(t *testing.T, want, flag string, metadata map[string]any) {
	t.Helper()

	if flag == "own" {
		assert.Equal(t, "its-own", metadata[statusKey], "a flag's own metadata is never removed or replaced")

		return
	}

	assertStatus(t, want, metadata, flag)
}

// wantFor is the status that a flag carries: only the beta flag has a segment.
func wantFor(flag, wantBeta string) string {
	if flag == betaFlag {
		return wantBeta
	}

	return ""
}
