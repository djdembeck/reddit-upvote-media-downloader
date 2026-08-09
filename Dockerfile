# Build argument for Alpine version - used in both builder and runtime stages
# Update this to change the base Alpine version for the entire image
ARG ALPINE_VERSION=3.20

# Build stage: compile Go binary with CGO enabled for SQLite support
FROM golang:1.23-alpine${ALPINE_VERSION} AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Version stamping: ARG VERSION is passed at build time (default "devel" for
# local and CI builds). -ldflags -X injects it into the version package's var.
ARG VERSION=devel

# Build with CGO enabled for SQLite
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo \
	-ldflags "-w -s -X github.com/djdembeck/reddit-upvote-media-downloader/internal/version.Version=${VERSION}" \
	-o reddit-downloader cmd/downloader/main.go

# Runtime stage: minimal Alpine image with required runtime dependencies
# Uses same ALPINE_VERSION as builder for compatibility
FROM alpine:${ALPINE_VERSION} AS runner

RUN apk --no-cache add \
    ca-certificates \
    sqlite-libs \
    su-exec \
    shadow

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/reddit-downloader .

# Copy entrypoint script
COPY entrypoint.sh .
RUN chmod +x entrypoint.sh

# Create non-root user and data directory
RUN addgroup -g 1000 appgroup && \
    adduser -D -u 1000 -G appgroup -h /home/appuser -s /sbin/nologin appuser && \
    mkdir -p /data/output && \
    chown -R appuser:appgroup /data

# Set environment defaults
ENV OUTPUT_DIR=/data/output
ENV DB_PATH=/data/posts.db
ENV CONCURRENCY=10
ENV FETCH_LIMIT=100
ENV DOWNLOAD_DELAY_MS=200ms
ENV LOG_LEVEL=info
ENV MIGRATE_ON_START=true
# File ownership — defaults to 1000:1000 so files are owned by appuser, not root
# Change to match your host user/group (find with: id -u / id -g)
ENV PUID=1000
ENV PGID=1000

ENTRYPOINT ["./entrypoint.sh"]
CMD ["./reddit-downloader"]
