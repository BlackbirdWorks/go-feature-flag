package controller

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/ffcontext"
)

func Test_assertRequest(t *testing.T) {
	t.Parallel()
	missing := echo.NewHTTPError(
		http.StatusBadRequest,
		"assertRequest: impossible to find evaluationContext in request")
	legacy := echo.NewHTTPError(http.StatusBadRequest, errLegacyUser)
	tests := []struct {
		name    string
		req     *model.AllFlagRequest
		wantErr *echo.HTTPError
	}{
		{name: "no request", req: nil, wantErr: missing},
		{name: "request without evaluation context", req: &model.AllFlagRequest{}, wantErr: missing},
		{
			name:    "legacy user only is rejected naming evaluationContext",
			req:     &model.AllFlagRequest{LegacyUser: map[string]any{"key": "my-key"}},
			wantErr: legacy,
		},
		{
			name: "legacy user next to a valid evaluation context is rejected",
			req: &model.AllFlagRequest{
				LegacyUser:        map[string]any{"key": "my-key"},
				EvaluationContext: &model.EvaluationContextRequest{Key: "my-key"},
			},
			wantErr: legacy,
		},
		{
			name: "valid evaluation context",
			req: &model.AllFlagRequest{
				EvaluationContext: &model.EvaluationContextRequest{Key: "my-key"},
			},
		},
		{
			name: "evaluation context with empty key is allowed",
			req: &model.AllFlagRequest{
				EvaluationContext: &model.EvaluationContextRequest{Key: ""},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantErr, assertRequest(tt.req))
		})
	}
	assert.Contains(t, errLegacyUser, "evaluationContext")
}

func Test_evaluationContextFromRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		req     *model.AllFlagRequest
		wantErr error
		want    ffcontext.Context
	}{
		{
			name: "no request",
			req:  nil,
			wantErr: echo.NewHTTPError(
				http.StatusBadRequest,
				"evaluationContextFromRequest: impossible to convert the request, evaluationContext missing"),
		},
		{
			name: "request without evaluation context",
			req:  &model.AllFlagRequest{},
			wantErr: echo.NewHTTPError(
				http.StatusBadRequest,
				"evaluationContextFromRequest: impossible to convert the request, evaluationContext missing"),
		},
		{
			name: "valid use-case with EvaluationContext",
			req: &model.AllFlagRequest{
				EvaluationContext: &model.EvaluationContextRequest{
					Key: "key-1",
					Custom: map[string]any{
						"anonymous":    false,
						"custom-field": true,
					},
				},
			},
			want: ffcontext.
				NewEvaluationContextBuilder("key-1").
				AddCustom("anonymous", false).
				AddCustom("custom-field", true).
				Build(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := evaluationContextFromRequest(tt.req)
			if tt.wantErr != nil {
				assert.Equal(t, tt.wantErr, err)

				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
