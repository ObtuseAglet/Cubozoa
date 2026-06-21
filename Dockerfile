# Cubozoa container image.
#
# Multi-stage: a CGO-free static binary is built, then dropped onto a minimal
# Alpine runtime that includes ffmpeg/ffprobe so transcoding and probing work
# out of the box. The binary is pure Go, so the image runs as an unprivileged
# user with no shared libraries to patch.

# --- build stage ---
FROM golang:1.25-alpine AS build

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN CGO_ENABLED=0 go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/cubozoa ./cmd/cubozoa

# --- runtime stage ---
FROM alpine:3.20

# ffmpeg/ffprobe enable transcoding + metadata probing; ca-certificates lets the
# optional TMDb provider make HTTPS requests; tini reaps child processes (e.g.
# ffmpeg) cleanly as PID 1.
RUN apk add --no-cache ffmpeg ca-certificates tini \
    && adduser -D -u 10001 cubozoa \
    && mkdir -p /data /media \
    && chown cubozoa /data

COPY --from=build /out/cubozoa /usr/local/bin/cubozoa

ENV CUBOZOA_BIND_ADDRESS=:8096 \
    CUBOZOA_DATA_DIR=/data \
    CUBOZOA_MEDIA_DIR=/media

VOLUME ["/data", "/media"]
EXPOSE 8096
USER cubozoa

# A simple liveness probe against the operational health endpoint.
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -qO- http://127.0.0.1:8096/health >/dev/null 2>&1 || exit 1

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/cubozoa"]
