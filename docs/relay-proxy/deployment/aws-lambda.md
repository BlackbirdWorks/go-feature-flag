# AWS Lambda

## Overview

The Semaphore relay proxy can run as an AWS Lambda function.

## Configure AWS Lambda mode

Set `startAsAwsLambda: true` and `awsLambdaAdapter` in your configuration file:

```yaml
# ...
startAsAwsLambda: true
awsLambdaAdapter: APIGatewayV2
# ...
```

## API Gateway non-root path configuration

If your Lambda function sits behind AWS API Gateway on a non-root path (e.g., `/api/feature-flags/{proxy+}`), set `awsApiGatewayBasePath` so the proxy strips the prefix before routing:

```yaml
# ...
startAsAwsLambda: true
awsLambdaAdapter: APIGatewayV2
awsApiGatewayBasePath: "/api/feature-flags"  # Strip this prefix from all requests
# ...
```

**Example:**

- API Gateway route: `/api/feature-flags/{proxy+}`
- Client request: `https://your-api.amazonaws.com/api/feature-flags/health`
- With `awsApiGatewayBasePath: "/api/feature-flags"`, the proxy routes `/health`

| Field name | Mandatory | Type | Default | Description |
|---|---|---|---|---|
| `startAsAwsLambda` | Yes | `boolean` | **`false`** | Set to `true` to run the relay proxy as an AWS Lambda function. |
| `awsLambdaAdapter` | No | `string` | **`APIGatewayV2`** | Used only when `startAsAwsLambda` is `true`. Accepted values: `APIGatewayV2`, `APIGatewayV1`, `ALB`. |
| `awsApiGatewayBasePath` | No | `string` | **none** | Used only when `startAsAwsLambda` is `true`. Path prefix stripped from incoming requests for non-root API Gateway routes (for example, `/api` or `/dev/feature-flags`). |

Once configured, deploy your function in AWS and expose it over HTTP via API Gateway or an Application Load Balancer (ALB).

## Lambda binary

Build a binary for the AWS `provided.al2023` runtime (`arm64`) from source:

```shell
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bootstrap ./cmd/relayproxy
zip semaphore-aws-lambda.zip bootstrap
```
