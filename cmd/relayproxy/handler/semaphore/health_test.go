package semaphore_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controller "github.com/blackbirdworks/semaphore/cmd/relayproxy/handler/semaphore"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/testdata/mock"
)

func Test_health_Handler(t *testing.T) {
	t.Parallel()

	type want struct {
		bodyFile   string
		httpCode   int
		handlerErr bool
	}

	tests := []struct {
		name string
		want want
	}{
		{
			name: "valid health",
			want: want{
				httpCode: http.StatusOK,
				bodyFile: testdataDir + "/health/valid.json",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := &mock.MockMonitoringService{HealthResponse: model.HealthResponse{Initialized: true}}
			healthCtrl := controller.NewHealth(srv)

			out, res := healthCtrl.Handle(t.Context(), &struct{}{})

			if tt.want.handlerErr {
				assert.Error(t, res, handlerShouldReturnAn)

				return
			}

			require.NoError(t, res)

			body, err := os.ReadFile(tt.want.bodyFile)
			require.NoError(t, err, "Impossible the expected body file %s", tt.want.bodyFile)
			got, err := json.Marshal(out.Body)
			require.NoError(t, err)
			assert.JSONEq(t, string(body), string(got), "Invalid response body")
		})
	}
}
