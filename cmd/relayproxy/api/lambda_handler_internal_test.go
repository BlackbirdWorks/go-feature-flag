package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service/stream"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/notifier"
)

const (
	applicationJson = "application/json"
	contentType     = "Content-Type"
	gET             = "GET"
	health2         = "/health"
)

func TestAwsLambdaHandler_GetAdapter(t *testing.T) {
	t.Parallel()

	type test struct {
		request    any
		name       string
		mode       string
		wantStatus int
	}

	tests := []test{
		{
			name:       "APIGatewayV2 event handler",
			mode:       "APIGatewayV2",
			wantStatus: http.StatusOK,
			request: events.APIGatewayV2HTTPRequest{
				RequestContext: events.APIGatewayV2HTTPRequestContext{
					HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
						Method: gET,
						Path:   health2,
					},
				},
				Headers: map[string]string{
					contentType: applicationJson,
				},
				Body: "",
			},
		},
		{
			name:       "APIGatewayV1 event handler",
			mode:       "APIGatewayV1",
			wantStatus: http.StatusOK,
			request: events.APIGatewayProxyRequest{
				HTTPMethod: gET,
				Path:       health2,
				RequestContext: events.APIGatewayProxyRequestContext{
					Path:       health2,
					HTTPMethod: gET,
				},
				Headers: map[string]string{
					contentType: applicationJson,
				},
				Body: "",
			},
		},
		{
			name:       "ALB event handler",
			mode:       "ALB",
			wantStatus: http.StatusOK,
			request: events.ALBTargetGroupRequest{
				HTTPMethod: gET,
				Path:       health2,
				Headers: map[string]string{
					contentType: applicationJson,
				},
				Body: "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logger := slog.New(slog.DiscardHandler)
			cfg := &config.Config{
				StartAsAwsLambda: true,    //nolint:staticcheck // SA1019: the test covers the deprecated setting
				AwsLambdaAdapter: tt.mode, //nolint:staticcheck // SA1019: the test covers the deprecated setting
				Retrievers: &[]retrieverconf.RetrieverConf{
					{
						Kind: "file",
						Path: "../../../testdata/flag-config.yaml",
					},
				},
			}
			flagsetManager, err := service.NewFlagsetManager(cfg, logger, []notifier.Notifier{}, nil, nil)
			require.NoError(t, err)

			apiServer := New(cfg, service.Services{
				MonitoringService: service.NewMonitoring(flagsetManager),
				WebsocketService:  stream.NewWebsocketService(),
				FlagsetManager:    flagsetManager,
				Metrics:           metric.Metrics{},
			}, logger)

			reqJSON, err := json.Marshal(tt.request)
			require.NoError(t, err)

			// Create a Lambda handler
			handler := lambda.NewHandler(apiServer.lambdaHandler())

			// Invoke the handler with the mock event
			response, err := handler.Invoke(context.Background(), reqJSON)
			require.NoError(t, err)

			switch strings.ToLower(tt.mode) {
			case "apigatewayv2":
				var res events.APIGatewayV2HTTPResponse

				err = json.Unmarshal(response, &res)
				require.NoError(t, err)
				require.Equal(t, tt.wantStatus, res.StatusCode)
			case "apigatewayv1":
				var res events.APIGatewayProxyResponse

				err = json.Unmarshal(response, &res)
				require.NoError(t, err)
				require.Equal(t, tt.wantStatus, res.StatusCode)
			case "alb":
				var res events.ALBTargetGroupResponse

				err = json.Unmarshal(response, &res)
				require.NoError(t, err)
				require.Equal(t, tt.wantStatus, res.StatusCode)
			default:
				require.Fail(t, "not implemented")
			}
		})
	}
}

func TestAwsLambdaHandler_BasePathSupport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		basePath     string
		requestPath  string
		expectedCode int
	}{
		{
			name:         "Health endpoint with /api base path",
			basePath:     "/api",
			requestPath:  "/api/health",
			expectedCode: 200,
		},
		{
			name:         "Health endpoint with /api/feature-flags base path",
			basePath:     "/api/feature-flags",
			requestPath:  "/api/feature-flags/health",
			expectedCode: 200,
		},
		{
			name:         "Info endpoint with base path",
			basePath:     "/dev",
			requestPath:  "/dev/info",
			expectedCode: 200,
		},
		{
			name:         "No base path configured - direct path",
			basePath:     "",
			requestPath:  health2,
			expectedCode: 200,
		},
		{
			name:         "No base path configured - direct path",
			basePath:     "",
			requestPath:  health2,
			expectedCode: 200,
		},
	}

	logger := slog.New(slog.DiscardHandler)

	flagsetManager, err := service.NewFlagsetManager(&config.Config{
		Retrievers: &[]retrieverconf.RetrieverConf{
			{
				Kind: "file",
				Path: "../../../testdata/flag-config.yaml",
			},
		},
	}, logger, nil, nil, nil)
	require.NoError(t, err)

	commonServices := service.Services{
		MonitoringService: service.NewMonitoring(flagsetManager),
		WebsocketService:  stream.NewWebsocketService(),
		FlagsetManager:    flagsetManager,
		Metrics:           metric.Metrics{},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &config.Config{
				StartAsAwsLambda:      true,           //nolint:staticcheck // SA1019: the test covers the deprecated setting
				AwsLambdaAdapter:      "APIGatewayV2", //nolint:staticcheck // SA1019: the test covers the deprecated setting
				AwsAPIGatewayBasePath: tt.basePath,    //nolint:staticcheck // SA1019: the test covers the deprecated setting
			}
			apiServer := New(c, commonServices, logger)

			request := events.APIGatewayV2HTTPRequest{
				RequestContext: events.APIGatewayV2HTTPRequestContext{
					HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
						Method: gET,
						Path:   tt.requestPath,
					},
				},
				Headers: map[string]string{
					contentType: applicationJson,
				},
				Body: "",
			}

			reqJSON, marshalErr := json.Marshal(request)
			require.NoError(t, marshalErr)

			// Create a Lambda handler
			handler := lambda.NewHandler(apiServer.lambdaHandler())

			// Invoke the handler with the mock event
			response, marshalErr := handler.Invoke(context.Background(), reqJSON)
			require.NoError(t, marshalErr)

			var res events.APIGatewayV2HTTPResponse

			marshalErr = json.Unmarshal(response, &res)
			require.NoError(t, marshalErr)
			require.Equal(t, tt.expectedCode, res.StatusCode)
		})
	}
}
