# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

FROM golang:1.26.4-alpine@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS builder

WORKDIR /app

# Cache module downloads separately from the source for faster rebuilds.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Version metadata is injected at build time (docker build --build-arg ...).
ARG VERSION=dev
ARG BUILD_TIME=unknown
# -trimpath strips local filesystem paths; -s -w drop the symbol/debug tables to
# shrink the binary. CGO is disabled for a fully static build.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
        -o bin/gitduppy ./cmd/server

# git and git-lfs are required at runtime by the mirroring engine, so a
# distroless/scratch base is not viable; a pinned Alpine keeps the image small.
FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
# safe.directory '*' is baked into the system git config so the appuser can always
# operate on mirrored repositories regardless of their on-disk owner (e.g. repos
# created by a different UID, or when running under a read-only root filesystem
# where a per-user ~/.gitconfig could not be written). It is written at build time
# so no runtime write to /etc/gitconfig is needed.
RUN apk --no-cache add \
        ca-certificates=20260611-r0 \
        git=2.54.0-r0 \
        git-lfs=3.7.1-r0 \
        su-exec=0.3-r0 \
        wget=1.25.0-r3 \
    && git config --system --add safe.directory '*'

WORKDIR /app
COPY --from=builder /app/bin/gitduppy .
COPY --from=builder /app/internal/web ./internal/web
COPY --from=builder /app/static ./static

# Create appuser with explicit UID/GID 1000 to keep host mount ownership sane.
#
# No hard `USER appuser` directive is set on purpose: the entrypoint must run as
# root to chown the bind-mounted volumes (whose host-side ownership is arbitrary)
# before dropping to appuser via su-exec. A USER directive would run the entrypoint
# unprivileged and leave freshly-created, root-owned bind mounts unwritable — which
# would break the dev compose. The server process therefore still runs as the
# unprivileged UID 1000 (via su-exec), matching the industry-standard privilege-drop
# pattern used by the official postgres/redis images.
# The storage tree matches config.yaml's storage.* paths (base_path, ssh_path,
# backup_path) and the compose bind mounts; pre-creating it keeps a plain
# `docker run` (no mounts) working and gives bind mounts sane image-side parents.
RUN addgroup -g 1000 -S appgroup && adduser -u 1000 -S appuser -G appgroup \
    && mkdir -p /app/storage/repos /app/storage/ssh /app/storage/backups \
    && chown -R appuser:appgroup /app

COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 8080

# Container-level liveness probe (compose/k8s may override with their own).
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD ["wget", "--quiet", "--tries=1", "--spider", "http://localhost:8080/api/v1/health/live"]

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["./gitduppy"]
