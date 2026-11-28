package ofrep_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/ofrep"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
	"github.com/blackbirdworks/semaphore/ffcontext"
	"github.com/blackbirdworks/semaphore/internal/flagstate"
	"github.com/blackbirdworks/semaphore/internal/protocol"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
	coremodel "github.com/blackbirdworks/semaphore/modules/core/model"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
)

type reasonStubFlagSet struct {
	service.FlagSet

	reason    flagdef.ResolutionReason
	errorCode flagdef.ErrorCode
}

func (s *reasonStubFlagSet) RawVariation(
	_ string,
	_ ffcontext.Context,
	_ any,
) (coremodel.RawVarResult, error) {
	return coremodel.RawVarResult{
		Value:         true,
		VariationType: "v1",
		Reason:        s.reason,
		ErrorCode:     s.errorCode,
	}, nil
}

func (s *reasonStubFlagSet) AllFlagsState(_ ffcontext.Context) flagstate.AllFlags {
	all := flagstate.NewAllFlags()
	all.AddFlag("test-flag", &flagstate.FlagState{
		Value:         true,
		VariationType: "v1",
		Reason:        s.reason,
		ErrorCode:     s.errorCode,
	})

	return all
}

func Test_OFREPReasonMapping_AllEngineReasons(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		reason            flagdef.ResolutionReason
		errorCode         flagdef.ErrorCode
		wantSingleReason  string
		wantBulkReason    string
		wantBulkErrorCode flagdef.ErrorCode
		wantSingleStatus  int
		wantOmitValue     bool
	}{
		{
			name:             "STATIC stays STATIC",
			reason:           flagdef.ReasonStatic,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonStatic,
			wantBulkReason:   flagdef.ReasonStatic,
		},
		{
			name:             "DEFAULT maps to STATIC",
			reason:           flagdef.ReasonDefault,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonStatic,
			wantBulkReason:   flagdef.ReasonStatic,
		},
		{
			name:             "TARGETING_MATCH stays TARGETING_MATCH",
			reason:           flagdef.ReasonTargetingMatch,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonTargetingMatch,
			wantBulkReason:   flagdef.ReasonTargetingMatch,
		},
		{
			name:             "SPLIT stays SPLIT",
			reason:           flagdef.ReasonSplit,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonSplit,
			wantBulkReason:   flagdef.ReasonSplit,
		},
		{
			name:             "TARGETING_MATCH_SPLIT maps to SPLIT",
			reason:           flagdef.ReasonTargetingMatchSplit,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonSplit,
			wantBulkReason:   flagdef.ReasonSplit,
		},
		{
			name:             "DISABLED stays DISABLED and omits value",
			reason:           flagdef.ReasonDisabled,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonDisabled,
			wantBulkReason:   flagdef.ReasonDisabled,
			wantOmitValue:    true,
		},
		{
			name:             "UNKNOWN stays UNKNOWN",
			reason:           flagdef.ReasonUnknown,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonUnknown,
			wantBulkReason:   flagdef.ReasonUnknown,
		},
		{
			name:             "OFFLINE maps to UNKNOWN",
			reason:           flagdef.ReasonOffline,
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonUnknown,
			wantBulkReason:   flagdef.ReasonUnknown,
		},
		{
			name:              "ERROR returns 400 on single and stays ERROR on bulk",
			reason:            flagdef.ReasonError,
			errorCode:         flagdef.ErrorCodeGeneral,
			wantSingleStatus:  http.StatusBadRequest,
			wantBulkReason:    flagdef.ReasonError,
			wantBulkErrorCode: flagdef.ErrorCodeGeneral,
			wantOmitValue:     true,
		},
		{
			name:             "empty reason maps to UNKNOWN",
			reason:           "",
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonUnknown,
			wantBulkReason:   flagdef.ReasonUnknown,
		},
		{
			name:             "unrecognized reason maps to UNKNOWN",
			reason:           "UNEXPECTED_REASON",
			wantSingleStatus: http.StatusOK,
			wantSingleReason: flagdef.ReasonUnknown,
			wantBulkReason:   flagdef.ReasonUnknown,
		},
	}

	const reqBody = `{"context":{"targetingKey":"user-1"}}`

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resolver := &mock.Resolver{
				Set: &reasonStubFlagSet{
					reason:    tt.reason,
					errorCode: tt.errorCode,
				},
				Name:    "default",
				Default: true,
			}
			ctrl := ofrep.NewOFREPEvaluate(resolver, &metric.Metrics{}, ofrep.EventStream{})
			mux := http.NewServeMux()
			mux.Handle(postOfrepVOneEvaluate, helper.Handle(ctrl.Evaluate, false))

			singleReq := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				ofrepEvaluatePrefix+"test-flag",
				strings.NewReader(reqBody),
			)
			singleReq.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

			singleRec := httptest.NewRecorder()
			mux.ServeHTTP(singleRec, singleReq)
			require.Equal(t, tt.wantSingleStatus, singleRec.Code)

			if tt.wantSingleStatus == http.StatusOK {
				var rawSingle map[string]any
				require.NoError(t, json.Unmarshal(singleRec.Body.Bytes(), &rawSingle))
				assert.Equal(t, tt.wantSingleReason, rawSingle["reason"])

				if tt.wantOmitValue {
					assert.NotContains(t, rawSingle, "value")
				} else {
					assert.Equal(t, true, rawSingle["value"])
				}
			} else {
				var errResp model.OFREPEvaluateResponseError
				require.NoError(t, json.Unmarshal(singleRec.Body.Bytes(), &errResp))
				assert.Equal(t, tt.errorCode, errResp.ErrorCode)
			}

			bulkReq := httptest.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				fixtures.OFREPEvaluateFlagsPath,
				strings.NewReader(reqBody),
			)
			bulkReq.Header.Set(protocol.HeaderContentType, protocol.MIMEApplicationJSON)

			bulkRec := httptest.NewRecorder()
			require.NoError(t, ctrl.BulkEvaluate(bulkRec, bulkReq))
			require.Equal(t, http.StatusOK, bulkRec.Code)

			var bulkRaw struct {
				Flags []map[string]any `json:"flags"`
			}
			require.NoError(t, json.Unmarshal(bulkRec.Body.Bytes(), &bulkRaw))
			require.Len(t, bulkRaw.Flags, 1)
			assert.Equal(t, tt.wantBulkReason, bulkRaw.Flags[0]["reason"])

			if tt.wantOmitValue {
				assert.NotContains(t, bulkRaw.Flags[0], "value")
			} else {
				assert.Equal(t, true, bulkRaw.Flags[0]["value"])
			}

			if tt.wantBulkErrorCode != "" {
				assert.Equal(t, tt.wantBulkErrorCode, bulkRaw.Flags[0]["errorCode"])
			}
		})
	}
}
