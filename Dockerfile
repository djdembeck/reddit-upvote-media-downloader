# Build stage
FROM golang:1.23-alpine AS builder

# Install build dependencies
RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with CGO enabled for SQLite
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -o reddit-downloader cmd/downloader/main.go

# Runtime stage
FROM alpine:latest

# Install ca-certificates, sqlite-libs, and su-exec for privilege dropping
# Use fixed Alpine version for reproducible builds
ARG ALPINE_VERSION=3.19
RUN apk --no-cache add ca-certificates sqlite-libs su-exec shadow

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/reddit-downloader .

# Copy entrypoint script
COPY entrypoint.sh .

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
