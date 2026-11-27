# Semaphore relay proxy

The relay proxy reads your flag sets from a control plane store (an S3 bucket or a directory), where a publisher
writes a manifest and the objects it lists, and exposes HTTP APIs to evaluate your flags. It speaks OFREP
(OpenFeature Remote Evaluation Protocol), so applications can use the standard OpenFeature SDKs.

## When should I use the relay proxy?
- You want to access your flags through an API instead of the Go module.
- Your application is not written in Go.
- You want to centralize the access to your flag configuration.

## Quick start

```shell
make build-relayproxy        # from the repository root; binary: out/bin/semaphore
./out/bin/semaphore --config=/path/to/bootstrap.yaml
```

The bootstrap file is version 2 (a version 1 file is refused). Its path is `--config` or `SEMAPHORE_CONFIG`. An
example with a file store is in `testdata/dockerhub-example`. `SIGHUP` reloads the file, `SIGTERM` and `SIGINT`
stop the relay in order.

Documentation:
- [Getting started](../../docs/relay-proxy/getting-started.md)
- [Installation](../../docs/relay-proxy/install.md)
- [Bootstrap file reference](../../docs/relay-proxy/bootstrap-config.md)
- [API endpoints](../../docs/relay-proxy/endpoints.md)

## Container image
`make build-relayproxy-docker` builds a local image from source. Released images are published as
`ghcr.io/blackbirdworks/semaphore` (after the first release, see [Releasing](../../docs/RELEASING.md)).
