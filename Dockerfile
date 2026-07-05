# syntax=docker/dockerfile:1

FROM golang:1.26.4-alpine AS builder

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
FROM alpine:3.20
RUN apk --no-cache add ca-certificates su-exec git git-lfs wget

WORKDIR /app
COPY --from=builder /app/bin/gitduppy .
COPY --from=builder /app/internal/web ./internal/web
COPY --from=builder /app/static ./static

# Create appuser with explicit UID/GID 1000 to keep host mount ownership sane.
RUN addgroup -g 1000 -S appgroup && adduser -u 1000 -S appuser -G appgroup \
    && mkdir -p /app/repos /app/keys /app/backups \
    && chown -R appuser:appgroup /app

COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 8080

# Container-level liveness probe (compose/k8s may override with their own).
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost:8080/api/v1/health/live || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["./gitduppy"]
