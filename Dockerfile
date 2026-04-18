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

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates sqlite-libs

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/reddit-downloader .

# Create data directory
RUN mkdir -p /data/output

# Set environment defaults
ENV OUTPUT_DIR=/data/output
ENV DB_PATH=/data/posts.db
ENV CONCURRENCY=10
ENV FETCH_LIMIT=100
ENV DOWNLOAD_DELAY_MS=200ms
ENV LOG_LEVEL=info
ENV MIGRATE_ON_START=true
# File ownership (change to match your host user/group, e.g. 1000:1000)
# Files will be owned by root if left at 0
ENV PUID=0
ENV PGID=0

# Run the downloader
CMD ["./reddit-downloader"]
