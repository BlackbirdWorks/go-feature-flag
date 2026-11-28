package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/modules/core/flagdef"
)

func TestOFREPEvaluateSuccessResponse_MarshalJSON(t *testing.T) {
	t.Parallel()

	const variantEmpty = "empty"

	tests := []struct {
		resp     model.OFREPEvaluateSuccessResponse
		name     string
		wantJSON string
	}{
		{
			name: "nil value omits value property",
			resp: model.OFREPEvaluateSuccessResponse{
				Key:     "disabled-flag",
				Reason:  flagdef.ReasonDisabled,
				Variant: "SdkDefault",
			},
			wantJSON: `{"key":"disabled-flag","reason":"DISABLED","variant":"SdkDefault"}`,
		},
		{
			name: "false boolean keeps value property",
			resp: model.OFREPEvaluateSuccessResponse{
				Key:     "bool-flag",
				Value:   false,
				Reason:  flagdef.ReasonStatic,
				Variant: "off",
			},
			wantJSON: `{"key":"bool-flag","value":false,"reason":"STATIC","variant":"off"}`,
		},
		{
			name: "zero integer keeps value property",
			resp: model.OFREPEvaluateSuccessResponse{
				Key:     "int-flag",
				Value:   0,
				Reason:  flagdef.ReasonStatic,
				Variant: "zero",
			},
			wantJSON: `{"key":"int-flag","value":0,"reason":"STATIC","variant":"zero"}`,
		},
		{
			name: "zero float keeps value property",
			resp: model.OFREPEvaluateSuccessResponse{
				Key:     "float-flag",
				Value:   0.0,
				Reason:  flagdef.ReasonStatic,
				Variant: "zero",
			},
			wantJSON: `{"key":"float-flag","value":0,"reason":"STATIC","variant":"zero"}`,
		},
		{
			name: "empty string keeps value property",
			resp: model.OFREPEvaluateSuccessResponse{
				Key:     "str-flag",
				Value:   "",
				Reason:  flagdef.ReasonStatic,
				Variant: variantEmpty,
			},
			wantJSON: `{"key":"str-flag","value":"","reason":"STATIC","variant":"empty"}`,
		},
		{
			name: "empty object keeps value property",
			resp: model.OFREPEvaluateSuccessResponse{
				Key:     "obj-flag",
				Value:   map[string]any{},
				Reason:  flagdef.ReasonStatic,
				Variant: variantEmpty,
			},
			wantJSON: `{"key":"obj-flag","value":{},"reason":"STATIC","variant":"empty"}`,
		},
		{
			name: "empty slice keeps value property",
			resp: model.OFREPEvaluateSuccessResponse{
				Key:     "arr-flag",
				Value:   []any{},
				Reason:  flagdef.ReasonStatic,
				Variant: variantEmpty,
			},
			wantJSON: `{"key":"arr-flag","value":[],"reason":"STATIC","variant":"empty"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(tt.resp)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantJSON, string(raw))
		})
	}
}

func TestOFREPFlagBulkEvaluateSuccessResponse_MarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		resp     model.OFREPFlagBulkEvaluateSuccessResponse
		name     string
		wantJSON string
	}{
		{
			name: "disabled bulk item omits value property",
			resp: model.OFREPFlagBulkEvaluateSuccessResponse{
				OFREPEvaluateSuccessResponse: model.OFREPEvaluateSuccessResponse{
					Key:    "disabled-flag",
					Reason: flagdef.ReasonDisabled,
				},
			},
			wantJSON: `{"key":"disabled-flag","reason":"DISABLED","variant":""}`,
		},
		{
			name: "error bulk item omits value property",
			resp: model.OFREPFlagBulkEvaluateSuccessResponse{
				OFREPEvaluateSuccessResponse: model.OFREPEvaluateSuccessResponse{
					Key:    "bucket-flag",
					Reason: flagdef.ReasonError,
				},
				ErrorCode:    flagdef.ErrorCodeTargetingKeyMissing,
				ErrorDetails: "Error: Empty targeting key",
			},
			wantJSON: `{"key":"bucket-flag","reason":"ERROR","variant":"",` +
				`"errorCode":"TARGETING_KEY_MISSING","errorDetails":"Error: Empty targeting key"}`,
		},
		{
			name: "falsy value bulk item keeps value property",
			resp: model.OFREPFlagBulkEvaluateSuccessResponse{
				OFREPEvaluateSuccessResponse: model.OFREPEvaluateSuccessResponse{
					Key:     "bool-flag",
					Value:   false,
					Reason:  flagdef.ReasonStatic,
					Variant: "off",
				},
			},
			wantJSON: `{"key":"bool-flag","value":false,"reason":"STATIC","variant":"off"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(tt.resp)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantJSON, string(raw))
		})
	}
}

func TestOFREPCommonResponseError_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  model.OFREPCommonResponseError
		want string
	}{
		{
			name: "formats error code and details",
			err: model.OFREPCommonResponseError{
				ErrorCode:    flagdef.ErrorCodeInvalidContext,
				ErrorDetails: "missing context",
			},
			want: "[INVALID_CONTEXT] missing context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.err.Error())
		})
	}
}
