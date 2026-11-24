package middleware_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	middleware2 "github.com/blackbirdworks/semaphore/cmd/relayproxy/api/middleware"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

func TestStreamAuthorizer(t *testing.T) {
	t.Parallel()

	type args struct {
		confAPIKey string
		urlAPIKey  string
	}
	tests := []struct {
		name       string
		args       args
		wantStatus int
		wantBody   string
		wantErr    error
	}{
		{
			name: "valid apiKey",
			args: args{
				confAPIKey: "valid-api-key",
				urlAPIKey:  "valid-api-key",
			},
			wantStatus: http.StatusOK,
			wantBody:   "Authorized",
		},
		{
			name: "invalid apiKey",
			args: args{
				confAPIKey: "valid-api-key",
				urlAPIKey:  "invalid-api-key",
			},
			wantStatus: http.StatusUnauthorized,
			wantErr:    echo.ErrUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			req := httptest.NewRequest(
				http.MethodGet,
				fmt.Sprintf("/stream?apiKey=%s", tt.args.urlAPIKey),
				nil,
			)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			conf := &config.Config{
				AuthorizedKeys: config.APIKeys{
					Evaluation: []string{tt.args.confAPIKey},
				},
			}
			conf.ForceReloadAPIKeys()
			middleware := middleware2.StreamAuthorizer(conf, nil)
			handler := middleware(func(c *echo.Context) error {
				return c.String(http.StatusOK, "Authorized")
			})

			err := handler(c)

			assert.Equal(t, tt.wantErr, err)
			if tt.wantErr != nil {
				var coder echo.HTTPStatusCoder
				require.ErrorAs(t, err, &coder)
				assert.Equal(t, tt.wantStatus, coder.StatusCode())
			} else {
				assert.Equal(t, tt.wantStatus, rec.Code)
			}
			assert.Equal(t, tt.wantBody, rec.Body.String())
		})
	}
}
