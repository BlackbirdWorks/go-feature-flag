# AWS Lambda

## Overview
The Semaphore relay proxy can easily be launched as an AWS Lambda function.

## How to start the relay proxy as an AWS Lambda function

To start Semaphore relay-proxy as a lambda you have to set the option `startAsAwsLambda` in your configuration file to `true`, 
and you have to set which lambda handler you want to use, by setting the option `awsLambdaAdapter`.

```yaml
# ...
startAsAwsLambda: true
awsLambdaAdapter: APIGatewayV2
# ...
```

## API Gateway Non-Root Path Configuration

If you're deploying your Lambda function behind AWS API Gateway with a non-root path (e.g., `/api/feature-flags/{proxy+}`), you'll need to configure the base path to ensure proper request routing:

```yaml
# ...
startAsAwsLambda: true
awsLambdaAdapter: APIGatewayV2
awsApiGatewayBasePath: "/api/feature-flags"  # Strip this prefix from all requests
# ...
```

**Example scenario:**
- Your API Gateway route: `/api/feature-flags/{proxy+}`
- Client request: `https://your-api.amazonaws.com/api/feature-flags/health`
- With `awsApiGatewayBasePath: "/api/feature-flags"` configured, the request will be processed as `/health`

This configuration is essential when using API Gateway stages or organizing your APIs with path-based routing.

| Field name              |    Mandatory     | Type      | Default            | Description                                                                                                                                                                                                                                             |
|-------------------------|:----------------:|-----------|--------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| **startAsAwsLambda**    |  Yes   | `boolean` | **`false`**        | Set to `true` if you want to start the Semaphore relay proxy as an AWS Lambda                                                                                                                                                                     |
| **awsLambdaAdapter**    | No | `string`  | **`APIGatewayV2`** | Used only when `startAsAwsLambda` is `true`. Selects the AWS Lambda handler type.<br/><br/>Accepted values: `APIGatewayV2`, `APIGatewayV1`, `ALB`. |
| **awsApiGatewayBasePath** | No | `string`  | **none**           | Used only when `startAsAwsLambda` is `true`. Base path prefix stripped from incoming requests for non-root API Gateway routes (for example, `/api` or `/dev/feature-flags`). |

Once you've updated your configuration file, you can deploy your function in AWS and configure it to be accessible
via HTTP. This can be achieved by creating an API Gateway or an Application Load Balancer (ALB) and linking it to
your Lambda function.

By configuring your Semaphore relay proxy to run as an AWS Lambda function, you can take advantage of many
benefits of serverless computing, including automatic scaling, reduced infrastructure costs, and simplified
deployment and management.

## Lambda Binary

Build a binary compatible with the AWS `provided.al2023` runtime (arm64) from source:

```shell
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bootstrap ./cmd/relayproxy
zip semaphore-aws-lambda.zip bootstrap
```
