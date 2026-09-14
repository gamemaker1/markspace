# /containerfile
# Builds the image of the Markspace server.
#
# Build it from the repository root:
#   podman build -f containerfile -t markspace .

FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27-alpine AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=devel

WORKDIR /source
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY sdk sdk

# the binary has no cgo dependency, so it cross-compiles for every platform
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.buildVersion=$VERSION" \
    -o /out/markspace ./cmd/markspace \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/markspace /usr/local/bin/markspace
# a named volume copies the owner of /data, so the server can write to it
COPY --from=build --chown=65532:65532 /out/data /data

ENV MARKSPACE_CONFIG=/data/markspace.yaml \
    MARKSPACE_DATA_DIR=/data \
    MARKSPACE_LISTEN=:8080

USER 65532:65532
VOLUME /data
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["markspace", "health"]

ENTRYPOINT ["markspace"]
CMD ["serve"]
