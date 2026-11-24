package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
)

// newAwsLambdaHandlerManager is creating a new awsLambdaHandler struct with the httpadapter
// to proxy all lambda event to the API handler.
func newAwsLambdaHandlerManager(handler http.Handler, basePath string) awsLambdaHandler {
	adapterAPIGtwV2 := httpadapter.NewV2(handler)
	adapterALB := httpadapter.NewALB(handler)
	adapterAPIGtwV1 := httpadapter.New(handler)

	// Configure base path stripping if a base path is provided
	if basePath != "" {
		adapterAPIGtwV2.StripBasePath(basePath)
		adapterALB.StripBasePath(basePath)
		adapterAPIGtwV1.StripBasePath(basePath)
	}

	return awsLambdaHandler{
		adapterAPIGtwV2: adapterAPIGtwV2,
		adapterALB:      adapterALB,
		adapterAPIGtwV1: adapterAPIGtwV1,
	}
}

type awsLambdaHandler struct {
	adapterAPIGtwV2 *httpadapter.HandlerAdapterV2
	adapterAPIGtwV1 *httpadapter.HandlerAdapter
	adapterALB      *httpadapter.HandlerAdapterALB
}

// SelectAdapter returns the appropriate adapter based on the mode.
func (h *awsLambdaHandler) SelectAdapter(mode string) any {
	switch strings.ToUpper(mode) {
	case strings.ToUpper(config.LambdaAdapterAPIGatewayV1):
		return h.HandlerAPIGatewayV1
	case strings.ToUpper(config.LambdaAdapterALB):
		return h.HandlerALB
	default:
		return h.HandlerAPIGatewayV2
	}
}

// HandlerAPIGatewayV2 is the function that proxy the lambda events to the API handler for API Gateway V2.
func (h *awsLambdaHandler) HandlerAPIGatewayV2(
	ctx context.Context,
	req events.APIGatewayV2HTTPRequest,
) (
	events.APIGatewayV2HTTPResponse, error) {
	return h.adapterAPIGtwV2.ProxyWithContext(ctx, req)
}

// HandlerAPIGatewayV1 is the function that proxy the lambda events to the API handler for API Gateway V1.
func (h *awsLambdaHandler) HandlerAPIGatewayV1(
	ctx context.Context,
	req events.APIGatewayProxyRequest,
) (
	events.APIGatewayProxyResponse, error) {
	return h.adapterAPIGtwV1.ProxyWithContext(ctx, req)
}

// HandlerALB is the function that proxy the lambda events to the API handler for API Gateway V1.
func (h *awsLambdaHandler) HandlerALB(ctx context.Context, req events.ALBTargetGroupRequest) (
	events.ALBTargetGroupResponse, error) {
	return h.adapterALB.ProxyWithContext(ctx, req)
}
