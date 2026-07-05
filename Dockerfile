FROM golang:1.26.4-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o bin/gitduppy ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates su-exec git git-lfs
WORKDIR /app
COPY --from=builder /app/bin/gitduppy .
COPY --from=builder /app/internal/web ./internal/web
COPY --from=builder /app/static ./static

# Create appuser with explicit UID 1000 and GID 1000 to facilitate host mount ownership
RUN addgroup -g 1000 -S appgroup && adduser -u 1000 -S appuser -G appgroup \
    && mkdir -p /app/repos /app/keys /app/backups \
    && chown -R appuser:appgroup /app

# Copy and set up the entrypoint script
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 8080
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["./gitduppy"]