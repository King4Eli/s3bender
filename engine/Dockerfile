# syntax=docker/dockerfile:1

# Build on the runner's native platform and cross-compile for the target - much faster than
# compiling under QEMU emulation for multi-arch images.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
# Pure-Go SQLite (modernc.org/sqlite) - no CGO, so this is a fully static binary.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/s3bender .

FROM alpine:3.22
# uid/gid 999 matches the previous (.NET) image, so an existing file-storage volume stays writable.
# Alpine ships gid 999 as "ping", which nothing here needs.
RUN delgroup ping \
    && addgroup -S -g 999 s3bender && adduser -S -D -H -u 999 -G s3bender s3bender \
    && mkdir -p /data/objects /data/db && chown -R s3bender:s3bender /data
COPY --from=build /out/s3bender /usr/local/bin/s3bender

ENV S3BENDER_STORAGE_ROOT=/data/objects \
    S3BENDER_DB_PATH=/data/db/s3bender.db
USER s3bender

EXPOSE 8080 8081
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/healthz || exit 1

ENTRYPOINT ["s3bender"]
