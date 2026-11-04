# Static Go binary on a small Alpine base. CI builds it for linux/arm64 (the
# Raspberry Pi) and linux/amd64 (the standby) and publishes it to ghcr.io;
# the Go stage runs on the build machine and cross-compiles.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY templates ./templates
COPY static ./static
ARG VERSION=docker
ARG TARGETOS TARGETARCH
RUN go vet ./... && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags "-s -w -X main.version=${VERSION}" -trimpath -o /out/blog .

FROM alpine:3.21
LABEL org.opencontainers.image.source=https://github.com/WhyElEss/go-blog
# tzdata: TZ= for publication dates and logs.
RUN apk add --no-cache tzdata
COPY --from=build /out/blog /usr/local/bin/blog

# Owned by 1000:1000 so a fresh named volume inherits a writable /data.
RUN mkdir -p /data && chown 1000:1000 /data
USER 1000:1000
ENV DATA_DIR=/data LISTEN_ADDR=:8096
VOLUME ["/data"]
EXPOSE 8096

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8096/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/blog"]
