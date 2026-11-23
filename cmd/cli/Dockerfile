FROM gcr.io/distroless/static-debian13:latest
ARG TARGETPLATFORM
COPY LICENSE NOTICE /licenses/
COPY rules/LICENSE /licenses/rules-LICENSE
ENTRYPOINT ["/semaphore-cli"]
COPY $TARGETPLATFORM/semaphore-cli /
