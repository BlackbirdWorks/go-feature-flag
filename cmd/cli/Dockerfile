# Release image of the CLI, built by GoReleaser: it only COPYs the binary it has already
# cross-compiled.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:8ce6dd87e9823c5b48dcfb2c8604714ccb351ce59607327bcc4957448da279b5
ARG TARGETPLATFORM
COPY LICENSE NOTICE /licenses/
COPY rules/LICENSE /licenses/rules-LICENSE
COPY $TARGETPLATFORM/semaphore-cli /semaphore-cli
USER 65532:65532
ENTRYPOINT ["/semaphore-cli"]
