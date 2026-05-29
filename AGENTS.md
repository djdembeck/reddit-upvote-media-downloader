# AGENTS.md - AI Assistant Guide for Reddit Media Downloader

This document provides guidance for AI agents working on the reddit-upvote-media-downloader project. It supplements the existing README.md with development-specific patterns, workflows, and best practices.

## Project Overview

**reddit-upvote-media-downloader** is a lightweight, efficient Reddit media downloader written in Go. It fetches upvoted and saved posts, downloads images and videos (including from external sites), and tracks downloads to avoid duplicates. Runs on a 1-hour Docker schedule.

### Core Purpose

- OAuth2 authentication with Reddit
- Concurrent media downloads (10 parallel by default)
- SQLite database for deduplication tracking

### Technology Stack

- **Language**: Go 1.23
- **Database**: SQLite (via `github.com/mattn/go-sqlite3`)
- **Key Dependencies**:
  - `golang.org/x/oauth2` - OAuth2 authentication
  - `golang.org/x/sync` - Semaphore for concurrency control
  - `github.com/joho/godotenv` - Environment variable loading

## Architecture

### Directory Structure

See `README.md` for the full directory tree.

### Key Patterns

- **Configuration**: See `internal/config/config.go`
- **OAuth2**: See `README.md` setup walkthrough and `internal/reddit/client.go`
- **Concurrency**: See `internal/downloader/downloader.go` (semaphore pattern)

## Common Tasks

| Task | File(s) |
|------|---------|
| Add new Reddit post type | `internal/reddit/post.go`, `internal/downloader/extractor.go` |
| Add support for new media host | `internal/downloader/extractor.go`, `internal/downloader/downloader.go` |
| Modify database schema | `internal/storage/post.go`, `internal/storage/db.go` |
| Add configuration option | `internal/config/config.go` |

## Configuration Files

See `README.md` for complete environment variable and Docker Compose documentation.

## Testing Guidelines

### Test File Structure

Tests follow the pattern `${filename}_test.go` alongside source files:

- `internal/downloader/downloader_test.go` tests `downloader.go`
- `internal/reddit/client_test.go` tests `client.go`
- `internal/storage/db_test.go` tests `db.go`
- `internal/migration/migration_test.go` tests migration logic

### Test Patterns

```go
func TestDownloadMedia(t *testing.T) {
    tests := []struct {
        name     string
        setup    func()
        teardown func()
        // ...
    }{
        { /* test case */ },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tt.setup()
            defer tt.teardown()
            // test logic
        })
    }
}
```

### Mock Patterns

For testing Reddit API calls, use table-driven tests with mock responses.

## Best Practices

### Code Style

- Follow Go best practices and standard formatting (`gofmt`)
- Use named return values only when beneficial
- Handle errors explicitly; don't ignore errors
- Use `context.Context` for cancelable operations

### Error Handling

- Always check and handle errors
- Wrap errors with context using `fmt.Errorf`
- Return errors from functions; don't panic in library code
- Use structured logging with different log levels

### Concurrency

- Use semaphores for limiting concurrent operations
- Always release semaphore resources in defer
- Use goroutines and channels for concurrent processing
- Avoid data races; use `-race` flag during testing

### Security

- Never commit credentials or API keys
- Use environment variables for sensitive data
- Validate all user inputs
- Use parameterized SQL queries to prevent injection

### Performance

- Respect rate limits from Reddit API
- Use appropriate concurrency levels (default: 10)
- Reuse database connections
- Clean up resources in defer blocks

## Docker Deployment

### Building Docker Image

```bash
docker build -t reddit-upvote-media-downloader:latest .
```

### Build Stages

The Dockerfile uses multi-stage builds:

1. **Build stage**: Compiles Go code with all dependencies
2. **Runtime stage**: Minimal image with binary only (~15MB)

### Running with Docker Compose

See `README.md` for complete docker-compose.yml example.

### Volume Mounts

```yaml
volumes:
  - ./data:/app/data      # Output media and database
  - ./data/output:/app/output  # Downloaded files
```

### Healthchecks

The container includes a healthcheck that verifies the SQLite database is accessible.

### Resource Limits

Recommended limits for production:
- CPU: 2 cores
- Memory: 512MB
- Storage: Depends on media volume

## Debugging

### Enable Debug Logging

```bash
docker-compose run -e LOG_LEVEL=debug reddit-downloader
```

Or in `.env`:
```
LOG_LEVEL=debug
```

### View Logs

```bash
# Docker Compose
docker-compose logs -f

# Docker directly
docker logs -f reddit-upvote-media-downloader
```

### Common Issues

1. **401 Unauthorized**: Check `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, credentials
2. **Rate limited**: Reduce `CONCURRENCY` or increase `DOWNLOAD_DELAY_MS`
3. **Missing files**: Run with `--re-check` to verify and re-download
4. **Database locked**: Ensure only one instance is running

### Database Inspection

```bash
# Access SQLite shell inside container
docker exec -it reddit-upvote-media-downloader sqlite3 /app/data/posts.db

# View schema
.sqlite> .schema

# View table data
.sqlite> SELECT * FROM posts LIMIT 10;
```

## Performance Tuning

### Concurrency

Default: 10 parallel downloads. Adjust based on:
- Reddit API rate limits
- Bandwidth
- Storage I/O

```bash
# Increase for faster downloads (risk of rate limiting)
CONCURRENCY=20

# Decrease for stable operation
CONCURRENCY=5
```

### Batch Size

```bash
# More posts per cycle (longer runs)
FETCH_LIMIT=200

# Faster cycles, less per run
FETCH_LIMIT=50
```

### Retry Configuration

```bash
# More aggressive retries
RETRY_THRESHOLD=5
BACKOFF_BASE=10s
BACKOFF_MAX=120s

# Fail fast
RETRY_THRESHOLD=1
```