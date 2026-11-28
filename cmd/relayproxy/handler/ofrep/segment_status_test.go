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

const betaFlag = "beta"

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

	tests := []struct {
		provider   segment.Provider
		name       string
		flag       string
		wantStatus string
		opts       []ofrep.Option
	}{
		{name: "store error", flag: betaFlag, provider: downSegmentProvider{}, wantStatus: "STORE_ERROR"},
		{name: "not configured", flag: betaFlag, wantStatus: "NOT_CONFIGURED"},
		{
			name: "healthy is not reported", flag: betaFlag,
			provider: segment.NewMemoryProvider(map[string][]string{"beta": {"user"}}),
		},
		{name: "a flag without a segment", flag: "plain", provider: downSegmentProvider{}},
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
			assertStatus(t, tt.wantStatus, one.Metadata)

			bulk := httptest.NewRecorder()
			bulkReq := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/ofrep/v1/evaluate/flags", strings.NewReader(body))
			bulkReq.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)
			require.NoError(t, ctrl.BulkEvaluate(bulk, bulkReq))

			var all model.OFREPBulkEvaluateSuccessResponse
			require.NoError(t, json.Unmarshal(bulk.Body.Bytes(), &all))

			for i := range all.Flags {
				if all.Flags[i].Key == tt.flag {
					assertStatus(t, tt.wantStatus, all.Flags[i].Metadata)
				}
			}
		})
	}
}

func assertStatus(t *testing.T, want string, metadata map[string]any) {
	t.Helper()

	if want == "" {
		assert.NotContains(t, metadata, "semaphore_segmentStatus")

		return
	}

	assert.Equal(t, want, metadata["semaphore_segmentStatus"])
}
